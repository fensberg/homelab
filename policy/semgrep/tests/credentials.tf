locals {
  # ok: a-connection-string-carries-no-credential-of-its-own
  # postgres://tofu:hunter2@db is how it must never be written
  # ruleid: a-connection-string-carries-no-credential-of-its-own
  written = "postgres://tofu:%s@%s:%d/%s"
  # ok: a-connection-string-carries-no-credential-of-its-own
  # ruleid: a-connection-string-with-a-user-part-is-written-somewhere
  format = "postgres://%s:%s@%s:%d/%s?sslmode=require"
  # ok: a-connection-string-carries-no-credential-of-its-own
  # ruleid: a-connection-string-with-a-user-part-is-written-somewhere
  templated = "https://${local.user}@example.invalid"
  # ok: a-connection-string-carries-no-credential-of-its-own
  plain = "https://example.invalid/path"
}
