package store

import (
	"log/slog"
)

// CheckpointReport is what PRAGMA wal_checkpoint answers with.
type CheckpointReport struct {
	// Blocked means a reader held the checkpoint back; the next one catches up.
	Blocked bool
	// WALPages is the log's length; CheckpointedPages is how much of it moved.
	WALPages          int
	CheckpointedPages int
}

// wantCheckpoint is called by the writer between transactions: copying frames costs a fsync and belongs beside the writer, but only a checkpoint on its own connection rewinds the file (documentation/benchmarks.md).
func (s *Store) wantCheckpoint() {
	s.commitsSinceCheckpoint++
	if s.commitsSinceCheckpoint < s.config.CheckpointEveryCommits {
		return
	}
	s.commitsSinceCheckpoint = 0

	// Never blocks: a checkpointer still working covers this commit too.
	select {
	case s.checkpointWanted <- struct{}{}:
	default:
	}

	if s.logExceedsLimit() {
		s.rewindTheLog()
	}
}

// rewindTheLog checkpoints on the writer's connection so the log restarts; it is the path someone waits behind, so it runs only once the log outgrew its limit (RESTART and TRUNCATE wait out every reader).
func (s *Store) rewindTheLog() {
	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()

	if _, err := s.write.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		s.logger.Warn("rewinding the log", slog.Any("error", err))
	}
}

// checkpointLoop copies frames back into the database on its own connection.
// Checkpointing takes the checkpointer lock and not the writer lock, so this runs
// beside the writer rather than in front of it.
func (s *Store) checkpointLoop() {
	defer close(s.checkpointerDone)

	for range s.checkpointWanted {
		s.copyOutTheLog()
	}
}

func (s *Store) copyOutTheLog() {
	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()

	var blocked int
	var report CheckpointReport
	err := s.checkpointConn.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").
		Scan(&blocked, &report.WALPages, &report.CheckpointedPages)
	if err != nil {
		s.logger.Warn("checkpoint", slog.Any("error", err))
		return
	}
	// Not a failure: a long read holds frames back and the next one takes them.
	if blocked != 0 {
		s.logger.Debug("checkpoint held back by a reader",
			slog.Int("wal_pages", report.WALPages),
			slog.Int("checkpointed_pages", report.CheckpointedPages))
	}
}

// logExceedsLimit reports whether the log has outgrown its bound.
func (s *Store) logExceedsLimit() bool {
	info, err := s.fs.Stat(s.config.Path + "-wal")
	if err != nil {
		return false
	}
	return info.Size > s.config.LogSizeLimitBytes
}
