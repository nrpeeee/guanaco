package main

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Windows refuses to replace a file while another handle has it open without
// FILE_SHARE_DELETE, and Go opens files without that flag. Our own lock-free
// readers (Store.Get/List), antivirus scanners and the search indexer all hold
// files open briefly, so a plain rename fails now and then with "Access is
// denied". Those holds last milliseconds: retry for up to about a second.

const (
	errAccessDenied     = syscall.Errno(5)
	errSharingViolation = syscall.Errno(32)
	errLockViolation    = syscall.Errno(33)
)

func replaceFile(from, to string) error {
	var err error
	delay := 5 * time.Millisecond
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Rename(from, to); err == nil || !transientRenameErr(err) {
			return err
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay *= 2
		}
	}
	return err
}

func transientRenameErr(err error) bool {
	return errors.Is(err, errAccessDenied) || errors.Is(err, errSharingViolation) || errors.Is(err, errLockViolation)
}
