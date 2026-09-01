package jobs

import (
	"database/sql"
	"time"

	"github.com/makearr/MediaNexus/internal/store/gen/jobsdb"
)

func convertJob(row jobsdb.Job) Job {
	return Job{
		ID:             row.ID,
		Type:           Type(row.Type),
		Pool:           Pool(row.Pool),
		ActionKey:      row.ActionKey.String,
		Priority:       Priority(row.Priority),
		Payload:        []byte(row.PayloadJson),
		State:          State(row.State),
		Mutating:       row.Mutating != 0,
		Attempts:       int(row.Attempts),
		MaxAttempts:    int(row.MaxAttempts),
		RunAfter:       time.Unix(row.RunAfter, 0).UTC(),
		LeaseOwner:     row.LeaseOwner.String,
		LeaseExpiresAt: unixOrZero(row.LeaseExpiresAt),
		DependsOn:      row.DependsOn.Int64,
		CorrelationID:  row.CorrelationID.String,
		UserID:         row.UserID.Int64,
		LastError:      row.LastError.String,
		CreatedAt:      time.Unix(row.CreatedAt, 0).UTC(),
		UpdatedAt:      time.Unix(row.UpdatedAt, 0).UTC(),
	}
}

func convertAttempt(row jobsdb.JobAttempt) Attempt {
	return Attempt{
		ID:         row.ID,
		JobID:      row.JobID,
		Attempt:    int(row.Attempt),
		LeaseOwner: row.LeaseOwner,
		StartedAt:  time.Unix(row.StartedAt, 0).UTC(),
		FinishedAt: unixOrZero(row.FinishedAt),
		Outcome:    row.Outcome.String,
		Error:      row.Error.String,
	}
}

func unixOrZero(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0).UTC()
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value != 0}
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
