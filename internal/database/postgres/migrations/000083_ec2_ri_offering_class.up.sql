-- offering_class controls whether EC2 Reserved Instances are purchased as
-- Convertible (exchangeable for different families/sizes/OS; default) or
-- Standard (~5% cheaper but locked to the exact instance type for the full
-- term). An empty value is treated as "convertible" in application code.
ALTER TABLE global_config
  ADD COLUMN IF NOT EXISTS offering_class TEXT NOT NULL DEFAULT 'convertible';
