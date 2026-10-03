/*
Copyright The Kubernetes Authors.

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

package podlogs

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// TestCopyPodLogsTerminalWatchError checks that a terminal watch failure does
// not turn the log collector into a busy loop. When the RetryWatcher gives up,
// it closes its result channel for good, and a receive on a closed channel is
// always ready, so the collector must park on it rather than call check() every
// iteration.
func TestCopyPodLogsTerminalWatchError(t *testing.T) {
	cs := fake.NewSimpleClientset()

	var listCount atomic.Int32
	cs.PrependReactor("list", "pods", func(clienttesting.Action) (bool, runtime.Object, error) {
		listCount.Add(1)
		return true, &v1.PodList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}}, nil
	})

	fw := watch.NewFake()
	cs.PrependWatchReactor("pods", func(clienttesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})

	if err := CopyAllLogs(t.Context(), cs, "ns", LogOutput{StatusWriter: io.Discard}); err != nil {
		t.Fatalf("CopyAllLogs: %v", err)
	}

	// A 410 Gone delivered as a watch error is never retried, so the RetryWatcher
	// closes its channel for good. Error blocks until it is consumed, so once it
	// returns the terminal path is underway.
	fw.Error(&metav1.Status{
		Status: metav1.StatusFailure,
		Reason: metav1.StatusReasonGone,
		Code:   http.StatusGone,
	})

	// With the fix, List calls stop once the loop parks on the closed channel;
	// a busy loop never stops. The 30s ticker cannot fire within the deadline,
	// so a count that is still growing means the collector is spinning.
	deadline := time.Now().Add(2 * time.Second)
	prev := int32(-1)
	for {
		cur := listCount.Load()
		if cur == prev {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("List kept being called after a terminal watch error (reached %d and still growing); the collector is busy-looping on the closed watch channel", cur)
		}
		prev = cur
		time.Sleep(50 * time.Millisecond)
	}
}
