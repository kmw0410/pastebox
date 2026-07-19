package internal

import "sync"

type lockManager struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.RWMutex
	refs int
}

func newLockManager() *lockManager {
	return &lockManager{
		locks: make(map[string]*refLock),
	}
}

func (lm *lockManager) Lock(key string) func() {
	return lm.lock(key, false)
}

func (lm *lockManager) RLock(key string) func() {
	return lm.lock(key, true)
}

func (lm *lockManager) lock(key string, readOnly bool) func() {
	lm.mu.Lock()

	lock := lm.locks[key]
	if lock == nil {
		lock = &refLock{}
		lm.locks[key] = lock
	}

	lock.refs++
	lm.mu.Unlock()

	if readOnly {
		lock.mu.RLock()
	} else {
		lock.mu.Lock()
	}

	return func() {
		if readOnly {
			lock.mu.RUnlock()
		} else {
			lock.mu.Unlock()
		}

		lm.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(lm.locks, key)
		}
		lm.mu.Unlock()
	}
}
