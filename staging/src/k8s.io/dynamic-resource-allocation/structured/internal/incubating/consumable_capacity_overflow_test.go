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

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func qty(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

// TestRoundUpRangeOverflow exercises roundUpRange with inputs that made the
// int64 min+step*n arithmetic wrap or divide by zero, across the integer and
// milli paths. Each must round up correctly, never returning a negative value
// or panicking.
func TestRoundUpRangeOverflow(t *testing.T) {
	const maxInt64 = "9223372036854775807"
	tests := []struct {
		name string
		req  *resource.Quantity
		min  *resource.Quantity
		step *resource.Quantity
		frac bool
		want *resource.Quantity
	}{
		{
			name: "integer path: 6E rounds up to 10E past MaxInt64",
			req:  qty("6E"), min: qty("0"), step: qty("5E"), want: qty("10E"),
		},
		{
			name: "integer path: step Value() is 0 (100E) must not divide by zero",
			req:  qty("1E"), min: qty("0"), step: qty("100E"), want: qty("100E"),
		},
		{
			name: "integer path: request exceeds MaxInt64 with an in-range step",
			req:  qty("100E"), min: qty("0"), step: qty("1"), want: qty("100E"),
		},
		{
			name: "integer path: rounds to exactly MaxInt64 on the fast path",
			req:  qty(maxInt64), min: qty("0"), step: qty("1"), want: qty(maxInt64),
		},
		{
			name: "milli path: fractional step with a request past MaxMilliValue",
			req:  qty("100E"), min: qty("0"), step: qty("500m"), frac: true, want: qty("100E"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("roundUpRange panicked (divide by zero / overflow): %v", r)
				}
			}()
			vr := &resourceapi.CapacityRequestPolicyRange{Min: tc.min, Step: tc.step}
			got := roundUpRange(tc.req, vr, tc.frac)
			if got.Sign() < 0 {
				t.Fatalf("roundUpRange returned a negative quantity %s (int64 overflow)", got.String())
			}
			if got.Cmp(*tc.want) != 0 {
				t.Fatalf("roundUpRange = %s, want %s", got.String(), tc.want.String())
			}
		})
	}
}
