package store

// HoldCopies makes every copy (backup or snapshot) run hook inside it, so a test can keep one open.
func (s *Store) HoldCopies(hook func()) { s.copyHook = hook }
