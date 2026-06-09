-- Fix service_configs term column default from 12 to 3.
-- Migration 000001 incorrectly set DEFAULT 12, but valid terms are 0, 1, or 3 (years).

-- Fix the column default for new rows
ALTER TABLE service_configs ALTER COLUMN term SET DEFAULT 3;

-- Fix any existing rows that were inserted with the old invalid default
UPDATE service_configs SET term = 3 WHERE term = 12;
