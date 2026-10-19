-- One schema per service: services never read each other's tables, they only refer to each other by ID.
CREATE SCHEMA IF NOT EXISTS document;
CREATE SCHEMA IF NOT EXISTS ocr;
CREATE SCHEMA IF NOT EXISTS layout;
CREATE SCHEMA IF NOT EXISTS print_job;
CREATE SCHEMA IF NOT EXISTS gateway;
