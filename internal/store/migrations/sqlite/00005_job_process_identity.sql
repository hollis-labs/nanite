-- +goose Up
ALTER TABLE jobs ADD COLUMN process_identity TEXT NOT NULL DEFAULT '';
