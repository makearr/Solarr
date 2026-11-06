-- name: ListDefaultLanguages :many
SELECT code FROM default_language ORDER BY ord;

-- name: ClearDefaultLanguages :exec
DELETE FROM default_language;

-- name: AddDefaultLanguage :exec
INSERT INTO default_language (code, ord) VALUES (?, ?);
