package internal

import "sync"

type lockManager struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.Mutex
	refs int
}

func newLockManager() *lockManager {
	return &lockManager{
		locks: make(map[string]*refLock),
	}
}

func (lm *lockManager) Lock(key string) func() {
	lm.mu.Lock()

	lock := lm.locks[key]
	if lock == nil {
		lock = &refLock{}
		lm.locks[key] = lock
	}

	lock.refs++
	lm.mu.Unlock()

	lock.mu.Lock()

	return func() {
		lock.mu.Unlock()

		lm.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(lm.locks, key)
		}
		lm.mu.Unlock()
	}
}
