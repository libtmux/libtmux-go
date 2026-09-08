package main

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	transactionLockMode     = 0o600
	transactionLockAttempts = 8
)

type transactionLock struct {
	file    *os.File
	binding destinationBinding
	aliases []*os.File
	gate    bool
}

var transactionLocks = struct {
	sync.Mutex
	active map[physicalIdentity]*transactionLock
}{active: map[physicalIdentity]*transactionLock{}}

var transactionProcessGate sync.Mutex

func transactionLockPath() (string, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(home) {
			return "", errors.New("home directory must be absolute")
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Clean(filepath.Join(root, "libtmux-mcp-dev", "swap", "state.lock")), nil
}

func inspectTransactionLockClaim() (pathClaim, error) {
	path, err := transactionLockPath()
	if err != nil {
		return pathClaim{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := validateLockDirectory(filepath.Dir(path), false); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return pathClaim{}, err
		}
		resolved, err := resolveProspectivePath(path)
		return pathClaim{owner: "transaction", kind: "lock", resolved: resolved}, err
	}
	if err != nil {
		return pathClaim{}, err
	}
	if err := validateLockInfo(info); err != nil {
		return pathClaim{}, err
	}
	binding, err := captureDestination(path)
	return lockClaim(binding), err
}

func acquireTransactionLock() (*transactionLock, error) {
	return acquireTransactionLockWith(openAndLockTransactionFile)
}

func acquireTransactionLockWith(
	open func(string, bool) (*os.File, bool, error),
) (*transactionLock, error) {
	transactionProcessGate.Lock()
	releaseGate := true
	defer func() {
		if releaseGate {
			transactionProcessGate.Unlock()
		}
	}()
	path, err := transactionLockPath()
	if err != nil {
		return nil, err
	}
	if err := validateLockDirectory(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	for range transactionLockAttempts {
		_, statErr := os.Lstat(path)
		create := errors.Is(statErr, os.ErrNotExist)
		if statErr != nil && !create {
			return nil, statErr
		}
		file, created, err := open(path, create)
		if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if created {
			err = errors.Join(file.Chmod(transactionLockMode), file.Sync())
		}
		lock := &transactionLock{file: file, gate: true}
		if err == nil {
			lock.binding, err = heldLockBinding(file)
		}
		if err == nil {
			transactionLocks.Lock()
			transactionLocks.active[lock.binding.Target] = lock
			transactionLocks.Unlock()
			releaseGate = false
			return lock, nil
		}
		_ = unlockAndCloseTransactionFile(file)
		return nil, err
	}
	return nil, errors.New("transaction lock path did not stabilize")
}

func planWithTransactionLock(
	dryRun bool,
	action func(pathClaim, func() error, bool) error,
) (err error) {
	claim, err := inspectTransactionLockClaim()
	if err != nil {
		return err
	}
	if err = action(claim, nil, false); err != nil || dryRun {
		return err
	}
	lock, err := acquireTransactionLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.close()) }()
	return action(lockClaim(lock.binding), lock.validate, true)
}

func heldLockBinding(file *os.File) (destinationBinding, error) {
	info, err := file.Stat()
	if err != nil {
		return destinationBinding{}, err
	}
	if err := validateLockInfo(info); err != nil {
		return destinationBinding{}, err
	}
	identity, err := physicalIdentityForFile(file)
	if err != nil {
		return destinationBinding{}, err
	}
	binding, err := captureDestination(file.Name())
	if err != nil {
		return destinationBinding{}, err
	}
	if binding.Target != identity || binding.Mode != transactionLockMode {
		return destinationBinding{}, errors.New("transaction lock changed while held")
	}
	return binding, nil
}

func (l *transactionLock) validate() error {
	current, err := heldLockBinding(l.file)
	if err == nil && !sameBinding(current, l.binding) {
		err = errors.New("transaction lock changed while held")
	}
	return err
}

func (l *transactionLock) close() error {
	err := unlockAndCloseTransactionFile(l.file)
	transactionLocks.Lock()
	delete(transactionLocks.active, l.binding.Target)
	aliases := l.aliases
	l.aliases = nil
	transactionLocks.Unlock()
	for _, alias := range aliases {
		err = errors.Join(err, alias.Close())
	}
	if l.gate {
		l.gate = false
		transactionProcessGate.Unlock()
	}
	return err
}

func retainTransactionLockAlias(lock *transactionLock, file *os.File) {
	lock.aliases = append(lock.aliases, file)
}

func retainIfActiveLockAlias(file *os.File) bool {
	identity, err := physicalIdentityForFile(file)
	if err != nil {
		return false
	}
	transactionLocks.Lock()
	defer transactionLocks.Unlock()
	lock := transactionLocks.active[identity]
	if lock == nil {
		return false
	}
	retainTransactionLockAlias(lock, file)
	return true
}

func lockClaim(binding destinationBinding) pathClaim {
	return pathClaim{
		owner: "transaction", kind: "lock",
		resolved: binding.Resolved, target: binding.Target,
	}
}

func validateLockInfo(info os.FileInfo) error {
	links, err := fileLinkCount(info)
	switch {
	case !info.Mode().IsRegular():
		return errors.New("transaction lock is not a regular file")
	case !exactMode(info, transactionLockMode):
		return errors.New("transaction lock is not private mode 0600")
	case err != nil:
		return err
	case links != 1:
		return errors.New("transaction lock has another filesystem link")
	case !fileOwnedByCurrentUser(info):
		return errors.New("transaction lock is not owned by the current user")
	default:
		return nil
	}
}

func validateLockDirectory(path string, create bool) error {
	namespace := filepath.Dir(path)
	if create {
		if err := os.MkdirAll(filepath.Dir(namespace), 0o700); err != nil {
			return err
		}
		for _, directory := range []string{namespace, path} {
			if err := createPrivateLockDirectory(directory); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validatePrivateLockDirectory(namespace); err != nil {
		return err
	}
	return validatePrivateLockDirectory(path)
}

func createPrivateLockDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return validatePrivateLockDirectory(path)
}

func validatePrivateLockDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || !exactMode(info, 0o700) || !fileOwnedByCurrentUser(info) {
		return errors.New("transaction lock directory is not private")
	}
	return nil
}

func exactMode(info os.FileInfo, expected os.FileMode) bool {
	const special = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return info.Mode()&(os.ModePerm|special) == expected
}

func resolveProspectivePath(path string) (string, error) {
	missing, existing := "", path
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return "", errors.New("transaction lock parent is not a directory")
			}
			resolved, err := filepath.EvalSymlinks(existing)
			return filepath.Clean(filepath.Join(resolved, missing)), err
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		missing = filepath.Join(filepath.Base(existing), missing)
		next := filepath.Dir(existing)
		if next == existing {
			return "", errors.New("transaction lock has no existing parent")
		}
		existing = next
	}
}
