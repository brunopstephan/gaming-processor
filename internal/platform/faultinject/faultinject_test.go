//go:build !integration && !e2e && !faultinject

package faultinject

import "testing"

func TestHitIsANoOpWithoutTheBuildTag(t *testing.T) {
	t.Setenv("FAULT", CrashAfterCommitBeforeDelete)
	if Enabled {
		t.Fatal("the default build must not enable fault injection")
	}
	Hit(CrashAfterCommitBeforeDelete) // must return: the process keeps running
	Hit(CrashAfterPublishBeforeMark)
}
