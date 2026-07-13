/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package incubating

import (
	"errors"
	"math"

	"gopkg.in/inf.v0"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// CmpRequestOverCapacity checks whether the new capacity request can be added within the given capacity,
// and checks whether the requested value is against the capacity requestPolicy.
func CmpRequestOverCapacity(currentConsumedCapacity ConsumedCapacity, deviceRequestCapacity *resourceapi.CapacityRequirements,
	allowMultipleAllocations *bool, capacity map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, allocatingCapacity ConsumedCapacity, fractionalCapacityRange bool) (bool, error) {
	if requestsContainNonExistCapacity(deviceRequestCapacity, capacity) {
		return false, errors.New("some requested capacity has not been defined")
	}
	clone := currentConsumedCapacity.Clone()
	for name, cap := range capacity {
		var requestedValPtr *resource.Quantity
		if deviceRequestCapacity != nil && deviceRequestCapacity.Requests != nil {
			if requestedVal, requestedFound := deviceRequestCapacity.Requests[name]; requestedFound {
				requestedValPtr = &requestedVal
			}
		}
		consumedCapacity := calculateConsumedCapacity(requestedValPtr, cap, fractionalCapacityRange)
		if violatesPolicy(consumedCapacity, cap.RequestPolicy, fractionalCapacityRange) {
			return false, nil
		}
		// If the current clone already contains an entry for this capacity, add the consumedCapacity to it.
		// Otherwise, initialize it with calculated consumedCapacity.
		if _, allocatedFound := clone[name]; allocatedFound {
			clone[name].Add(consumedCapacity)
		} else {
			clone[name] = ptr.To(consumedCapacity)
		}
		// If allocatingCapacity contains an entry for this capacity, add its value to clone as well.
		if allocatingVal, allocatingFound := allocatingCapacity[name]; allocatingFound {
			clone[name].Add(*allocatingVal)
		}
		if clone[name].Cmp(cap.Value) > 0 {
			return false, nil
		}
	}
	return true, nil
}

// requestsNonExistCapacity returns true if requests contain non-exist capacity.
func requestsContainNonExistCapacity(deviceRequestCapacity *resourceapi.CapacityRequirements,
	capacity map[resourceapi.QualifiedName]resourceapi.DeviceCapacity) bool {
	if deviceRequestCapacity == nil || deviceRequestCapacity.Requests == nil {
		return false
	}
	for name := range deviceRequestCapacity.Requests {
		if _, found := capacity[name]; !found {
			return true
		}
	}
	return false
}

// calculateConsumedCapacity returns valid capacity to be consumed regarding the requested capacity and device capacity policy.
//
// If no requestPolicy, return capacity.Value.
// If no requestVal, fill the quantity by fillEmptyRequest function
// Otherwise, use requestPolicy to calculate the consumed capacity from request if applicable.
func calculateConsumedCapacity(requestedVal *resource.Quantity, capacity resourceapi.DeviceCapacity, fractionalCapacityRange bool) resource.Quantity {
	if requestedVal == nil {
		return fillEmptyRequest(capacity)
	}
	if capacity.RequestPolicy == nil {
		return requestedVal.DeepCopy()
	}
	switch {
	case capacity.RequestPolicy.ValidRange != nil && capacity.RequestPolicy.ValidRange.Min != nil:
		return roundUpRange(requestedVal, capacity.RequestPolicy.ValidRange, fractionalCapacityRange)
	case capacity.RequestPolicy.ValidValues != nil:
		return roundUpValidValues(requestedVal, capacity.RequestPolicy.ValidValues)
	}
	return *requestedVal
}

// fillEmptyRequest
// return requestPolicy.default if defined.
// Otherwise, return capacity value.
func fillEmptyRequest(capacity resourceapi.DeviceCapacity) resource.Quantity {
	if capacity.RequestPolicy != nil && capacity.RequestPolicy.Default != nil {
		return capacity.RequestPolicy.Default.DeepCopy()
	}
	return capacity.Value.DeepCopy()
}

// roundUpRange rounds the requestedVal up to fit within the specified validRange.
//   - If requestedVal is less than Min, it returns Min.
//   - If Step is specified, it rounds requestedVal up to the nearest multiple of Step
//     starting from Min.
//   - If no Step is specified and requestedVal >= Min, it returns requestedVal as is.
//
// When fractionalCapacityRange is true and any of min/max/step are fractional and all
// fit within the milli-value int64 range and step >= 1m, milli-value arithmetic is used.
// Otherwise Value() arithmetic is used.
func roundUpRange(requestedVal *resource.Quantity, validRange *resourceapi.CapacityRequestPolicyRange, fractionalCapacityRange bool) resource.Quantity {
	if requestedVal.Cmp(*validRange.Min) < 0 {
		return validRange.Min.DeepCopy()
	}
	if validRange.Step == nil {
		return *requestedVal
	}
	if useMilli(validRange, fractionalCapacityRange) {
		requestedMilli := requestedVal.MilliValue()
		stepMilli := validRange.Step.MilliValue()
		minMilli := validRange.Min.MilliValue()
		addedMilli := requestedMilli - minMilli
		n := addedMilli / stepMilli
		if addedMilli%stepMilli != 0 {
			n++
		}
		valMilli := minMilli + stepMilli*n
		// Return in the same format as the step quantity. If the result is a
		// whole number, use NewQuantity to keep the representation compact and
		// compatible with quantities parsed from whole-number strings.
		format := validRange.Step.Format
		if valMilli%1000 == 0 {
			return *resource.NewQuantity(valMilli/1000, format)
		}
		return *resource.NewMilliQuantity(valMilli, format)
	}
	// Integer arithmetic path. Prefer the int64 fast path, which preserves the
	// canonical (integer) representation of in-range results; fall back to
	// arbitrary-precision arithmetic when an operand does not fit int64 (Value()
	// would truncate) or when min+step*n would overflow, so large capacities round
	// up correctly instead of wrapping negative. The fast path requires Min >= 0
	// (with requestedVal >= Min, checked above) so that added and the
	// (maxI64 - minInt) guard cannot overflow.
	maxI64 := int64(math.MaxInt64)
	if validRange.Min.Sign() >= 0 &&
		requestedVal.CmpInt64(maxI64) <= 0 &&
		validRange.Step.CmpInt64(maxI64) <= 0 &&
		validRange.Min.CmpInt64(maxI64) <= 0 {
		requestedInt := requestedVal.Value()
		stepInt := validRange.Step.Value()
		minInt := validRange.Min.Value()
		added := requestedInt - minInt
		n := added / stepInt
		if added%stepInt != 0 {
			n++
		}
		if n == 0 || stepInt <= (maxI64-minInt)/n {
			return *resource.NewQuantity(minInt+stepInt*n, validRange.Step.Format)
		}
	}
	requestedDec := requestedVal.AsDec()
	stepDec := validRange.Step.AsDec()
	minDec := validRange.Min.AsDec()
	addedDec := new(inf.Dec).Sub(requestedDec, minDec)
	n := new(inf.Dec).QuoRound(addedDec, stepDec, 0, inf.RoundCeil)
	result := new(inf.Dec).Add(minDec, new(inf.Dec).Mul(stepDec, n))
	return *resource.NewDecimalQuantity(*result, validRange.Step.Format)
}

// roundUpValidValues returns the first value in validValues that is greater than or equal to requestedVal.
// If no such value exists, it returns requestedVal itself.
func roundUpValidValues(requestedVal *resource.Quantity, validValues []resource.Quantity) resource.Quantity {
	// Simple sequential search is used as the maximum entry of validValues is finite and small (≤10),
	// and the list must already be sorted in ascending order, ensured by API validation.
	// Note: A binary search could alternatively be used for better efficiency if the list grows larger.
	for _, validValue := range validValues {
		if requestedVal.Cmp(validValue) <= 0 {
			return validValue.DeepCopy()
		}
	}
	return *requestedVal
}

// GetConsumedCapacityFromRequest returns valid consumed capacity,
// according to claim request and defined capacity.
func GetConsumedCapacityFromRequest(requestedCapacity *resourceapi.CapacityRequirements,
	consumableCapacity map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, fractionalCapacityRange bool) map[resourceapi.QualifiedName]resource.Quantity {
	consumedCapacity := make(map[resourceapi.QualifiedName]resource.Quantity)
	for name, cap := range consumableCapacity {
		var requestedValPtr *resource.Quantity
		if requestedCapacity != nil && requestedCapacity.Requests != nil {
			if requestedVal, requestedFound := requestedCapacity.Requests[name]; requestedFound {
				requestedValPtr = &requestedVal
			}
		}
		capacity := calculateConsumedCapacity(requestedValPtr, cap, fractionalCapacityRange)
		consumedCapacity[name] = capacity
	}
	return consumedCapacity
}

// violatesPolicy checks whether the request violate the requestPolicy.
func violatesPolicy(requestedVal resource.Quantity, policy *resourceapi.CapacityRequestPolicy, fractionalCapacityRange bool) bool {
	if policy == nil {
		// no policy to check
		return false
	}
	if policy.Default != nil && requestedVal == *policy.Default {
		return false
	}
	switch {
	case policy.ValidRange != nil:
		return violateValidRange(requestedVal, *policy.ValidRange, fractionalCapacityRange)
	case len(policy.ValidValues) > 0:
		return violateValidValues(requestedVal, policy.ValidValues)
	}
	// no policy violated through to completion.
	return false
}

func violateValidRange(requestedVal resource.Quantity, validRange resourceapi.CapacityRequestPolicyRange, fractionalCapacityRange bool) bool {
	if validRange.Max != nil &&
		requestedVal.Cmp(*validRange.Max) > 0 {
		return true
	}
	if validRange.Step != nil {
		var requested, step, min int64
		if useMilli(&validRange, fractionalCapacityRange) {
			requested = requestedVal.MilliValue()
			step = validRange.Step.MilliValue()
			min = validRange.Min.MilliValue()
		} else {
			requested = requestedVal.Value()
			step = validRange.Step.Value()
			min = validRange.Min.Value()
		}
		// must be a multiple of step from min
		if (requested-min)%step != 0 {
			return true
		}
	}
	return false
}

// useMilli reports whether milli-value arithmetic should be used for the given range.
// Conditions: fractionalCapacityRange enabled AND any of min/max/step is fractional
// AND all non-nil fields fit within the milli-value int64 range AND step >= 1m.
func useMilli(validRange *resourceapi.CapacityRequestPolicyRange, fractionalCapacityRange bool) bool {
	if !fractionalCapacityRange {
		return false
	}
	hasFractional := false
	for _, q := range []*resource.Quantity{validRange.Min, validRange.Max, validRange.Step} {
		if q == nil {
			continue
		}
		if q.Value() > resource.MaxMilliValue {
			return false
		}
		if q.MilliValue()%1000 != 0 {
			hasFractional = true
		}
	}
	if !hasFractional {
		return false
	}
	// Step must be at least 1m.
	if validRange.Step != nil && validRange.Step.MilliValue() < 1 {
		return false
	}
	return true
}

func violateValidValues(requestedVal resource.Quantity, validValues []resource.Quantity) bool {
	for _, validVal := range validValues {
		if requestedVal.Cmp(validVal) == 0 {
			return false
		}
	}
	return true
}
