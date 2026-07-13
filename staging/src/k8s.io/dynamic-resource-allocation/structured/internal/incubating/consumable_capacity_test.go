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
	"testing"

	. "github.com/onsi/gomega"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	driverA   = "driver-a"
	pool1     = "pool-1"
	device1   = "device-1"
	capacity0 = "capacity-0"
	capacity1 = "capacity-1"
)

var (
	one   = resource.MustParse("1")
	two   = resource.MustParse("2")
	three = resource.MustParse("3")

	pointTwoFive = resource.MustParse("250m")
	pointFour    = resource.MustParse("400m")
	pointThree   = resource.MustParse("300m")
	pointTwo     = resource.MustParse("200m")
	pointOne     = resource.MustParse("100m")
)

func deviceConsumedCapacity(deviceID DeviceID) DeviceConsumedCapacity {
	capaicty := map[resourceapi.QualifiedName]resource.Quantity{
		capacity0: one,
	}
	return NewDeviceConsumedCapacity(deviceID, capaicty)
}

func TestConsumableCapacity(t *testing.T) {

	t.Run("add-sub-allocating-consumed-capacity", func(t *testing.T) {
		g := NewWithT(t)
		allocatedCapacity := NewConsumedCapacity()
		g.Expect(allocatedCapacity.Empty()).To(BeTrueBecause("allocated capacity should start from zero"))
		oneAllocated := ConsumedCapacity{
			capacity0: &one,
		}
		allocatedCapacity.Add(oneAllocated)
		g.Expect(allocatedCapacity.Empty()).To(BeFalseBecause("capacity is added"))
		allocatedCapacity.Sub(oneAllocated)
		g.Expect(allocatedCapacity.Empty()).To(BeTrueBecause("capacity is subtracted to zero"))
	})

	t.Run("insert-remove-allocating-consumed-capacity-collection", func(t *testing.T) {
		g := NewWithT(t)
		deviceID := MakeDeviceID(driverA, pool1, device1)
		aggregatedCapacity := NewConsumedCapacityCollection()
		aggregatedCapacity.Insert(deviceConsumedCapacity(deviceID))
		aggregatedCapacity.Insert(deviceConsumedCapacity(deviceID))
		allocatedCapacity, found := aggregatedCapacity[deviceID]
		g.Expect(found).To(BeTrueBecause("expected deviceID to be found"))
		g.Expect(allocatedCapacity[capacity0].Cmp(two)).To(BeZero())
		aggregatedCapacity.Remove(deviceConsumedCapacity(deviceID))
		g.Expect(allocatedCapacity[capacity0].Cmp(one)).To(BeZero())
	})

	t.Run("get-consumed-capacity-from-request", func(t *testing.T) {
		requestedCapacity := &resourceapi.CapacityRequirements{
			Requests: map[resourceapi.QualifiedName]resource.Quantity{
				capacity0: one,
				"dummy":   one,
			},
		}
		consumableCapacity := map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
			capacity0: { // with request and with default, expect requested value
				Value: two,
				RequestPolicy: &resourceapi.CapacityRequestPolicy{
					Default:    &two,
					ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one},
				},
			},
			capacity1: { // no request but with default, expect default
				Value: two,
				RequestPolicy: &resourceapi.CapacityRequestPolicy{
					Default:    &one,
					ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one},
				},
			},
			"dummy": {
				Value: one, // no request and no policy (no default), expect capacity value
			},
		}
		consumedCapacity := GetConsumedCapacityFromRequest(requestedCapacity, consumableCapacity, false)
		g := NewWithT(t)
		g.Expect(consumedCapacity).To(HaveLen(3))
		for name, val := range consumedCapacity {
			g.Expect(string(name)).Should(BeElementOf([]string{capacity0, capacity1, "dummy"}))
			g.Expect(val.Cmp(one)).To(BeZero())
		}
	})

	t.Run("violate-capacity-sharing", testViolateCapacityRequestPolicy)

	t.Run("calculate-consumed-capacity", testCalculateConsumedCapacity)

}

func testViolateCapacityRequestPolicy(t *testing.T) {
	testcases := map[string]struct {
		requestedVal            resource.Quantity
		requestPolicy           *resourceapi.CapacityRequestPolicy
		fractionalCapacityRange bool
		expectResult            bool
	}{
		"no constraint": {requestedVal: one, expectResult: false},
		"less than maximum": {
			requestedVal: one,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default:    &one,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one, Max: &two},
			},
			expectResult: false,
		},
		"more than maximum": {
			requestedVal: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default:    &one,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one, Max: &one},
			},
			expectResult: true,
		},
		"in set": {
			requestedVal: one,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default:     &one,
				ValidValues: []resource.Quantity{one},
			},
			expectResult: false,
		},
		"not in set": {
			requestedVal: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default:     &one,
				ValidValues: []resource.Quantity{one},
			},
			expectResult: true,
		},
		// fractional step: min=0.2, step=0.1, max=1
		"fractional step aligned (0.3 = min+1*step)": {
			requestedVal: pointThree,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default: &pointTwo,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{
					Min:  &pointTwo,
					Max:  &one,
					Step: &pointOne,
				},
			},
			fractionalCapacityRange: true,
			expectResult:            false,
		},
		"fractional step not aligned (0.25 is not a multiple of 0.1 from 0.2)": {
			requestedVal: pointTwoFive,
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default: &pointTwo,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{
					Min:  &pointTwo,
					Max:  &one,
					Step: &pointOne,
				},
			},
			fractionalCapacityRange: true,
			expectResult:            true,
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			violate := violatesPolicy(tc.requestedVal, tc.requestPolicy, tc.fractionalCapacityRange)
			g.Expect(violate).To(BeEquivalentTo(tc.expectResult))
		})
	}
}

func testCalculateConsumedCapacity(t *testing.T) {
	testcases := map[string]struct {
		requestedVal            *resource.Quantity
		capacityValue           resource.Quantity
		requestPolicy           *resourceapi.CapacityRequestPolicy
		fractionalCapacityRange bool
		expectResult            resource.Quantity
	}{
		"empty": {requestedVal: nil, capacityValue: one, requestPolicy: &resourceapi.CapacityRequestPolicy{}, expectResult: one},
		"min in range": {
			requestedVal:  nil,
			capacityValue: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one}},
			expectResult:  one,
		},
		"default in set": {
			requestedVal:  nil,
			capacityValue: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidValues: []resource.Quantity{one}},
			expectResult:  one,
		},
		"more than min in range": {
			requestedVal:  &two,
			capacityValue: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one}},
			expectResult:  two,
		},
		"less than min in range": {
			requestedVal:  &one,
			capacityValue: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &two}},
			expectResult:  two,
		},
		"with step (round up)": {
			requestedVal:  &two,
			capacityValue: three,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one, Step: &two}},
			expectResult:  three,
		},
		"with step (no remaining)": {
			requestedVal:  &two,
			capacityValue: two,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidRange: &resourceapi.CapacityRequestPolicyRange{Min: &one, Step: &one}},
			expectResult:  two,
		},
		// fractional step: min=0.2, step=0.1, max=1; request=0.25; rounds up to 0.3
		"fractional step round up (0.25 to 0.3)": {
			requestedVal:  &pointTwoFive,
			capacityValue: resource.MustParse("1"),
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default: &pointTwo,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{
					Min:  &pointTwo,
					Max:  &one,
					Step: &pointOne,
				},
			},
			fractionalCapacityRange: true,
			expectResult:            resource.MustParse("300m"),
		},
		// fractional step: request already aligned; no rounding
		"fractional step already aligned (0.4 = min+2*step)": {
			requestedVal:  &pointFour,
			capacityValue: resource.MustParse("1"),
			requestPolicy: &resourceapi.CapacityRequestPolicy{
				Default: &pointTwo,
				ValidRange: &resourceapi.CapacityRequestPolicyRange{
					Min:  &pointTwo,
					Max:  &one,
					Step: &pointOne,
				},
			},
			fractionalCapacityRange: true,
			expectResult:            resource.MustParse("400m"),
		},
		"valid value in set": {
			requestedVal:  &two,
			capacityValue: three,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidValues: []resource.Quantity{one, two, three}},
			expectResult:  two,
		},
		"set (round up)": {
			requestedVal:  &two,
			capacityValue: three,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidValues: []resource.Quantity{one, three}},
			expectResult:  three,
		},
		"larger than set": {
			requestedVal:  &three,
			capacityValue: three,
			requestPolicy: &resourceapi.CapacityRequestPolicy{Default: &one, ValidValues: []resource.Quantity{one, two}},
			expectResult:  three,
		},
	}
	for name, tc := range testcases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			capacity := resourceapi.DeviceCapacity{
				Value:         tc.capacityValue,
				RequestPolicy: tc.requestPolicy,
			}
			consumedCapacity := calculateConsumedCapacity(tc.requestedVal, capacity, tc.fractionalCapacityRange)
			g.Expect(consumedCapacity.Cmp(tc.expectResult)).To(BeZero())
		})
	}
}

// TestRoundUpRangeIntegerOverflow guards the integer arithmetic path of roundUpRange
// against int64 overflow. Rounding up to min+step*n, or reading an operand through
// Quantity.Value(), previously overflowed or truncated int64 and wrapped the rounded
// capacity to a negative value for large quantities.
// Regression test for https://github.com/kubernetes/kubernetes/issues/140441.
func TestRoundUpRangeIntegerOverflow(t *testing.T) {
	zero := resource.MustParse("0")
	for name, tc := range map[string]struct {
		step, request, want resource.Quantity
	}{
		// min+step*n overflows int64 even though every operand fits in int64:
		// ceil(6E/5E) = 2, and 2*5E = 10E = 1e19, which is greater than MaxInt64.
		"result overflows int64": {
			step:    resource.MustParse("5E"),
			request: resource.MustParse("6E"),
			want:    resource.MustParse("10E"),
		},
		// operands exceed int64, so Value() would truncate them:
		// ceil(150E/100E) = 2, and 2*100E = 200E.
		"operands exceed int64": {
			step:    resource.MustParse("100E"),
			request: resource.MustParse("150E"),
			want:    resource.MustParse("200E"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			validRange := &resourceapi.CapacityRequestPolicyRange{Min: &zero, Step: &tc.step}
			got := roundUpRange(&tc.request, validRange, false)
			g.Expect(got.Sign()).To(BeNumerically(">", 0), "rounded capacity wrapped negative: %s", got.String())
			g.Expect(got.Cmp(tc.request)).To(BeNumerically(">=", 0), "rounded capacity %s is less than request %s", got.String(), tc.request.String())
			g.Expect(got.Cmp(tc.want)).To(BeZero(), "got %s, want %s", got.String(), tc.want.String())
		})
	}
}
