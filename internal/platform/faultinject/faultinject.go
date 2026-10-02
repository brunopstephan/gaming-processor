// Package faultinject crashes the process at named points so end-to-end
// tests can prove recovery (spec §16). Only a binary built with the
// faultinject tag can crash; the normal build compiles Hit to nothing, so
// setting FAULT in production has no effect.
package faultinject

// Named crash points.
const (
	// CrashAfterCommitBeforeDelete: the SQS consumer committed the handling
	// of a message and is about to delete it.
	CrashAfterCommitBeforeDelete = "crash_after_commit_before_delete"
	// CrashAfterPublishBeforeMark: the outbox relay published an event and
	// is about to mark it published.
	CrashAfterPublishBeforeMark = "crash_after_publish_before_mark"
)

// ExitCode is the exit status of a process killed by an injected fault.
const ExitCode = 86
