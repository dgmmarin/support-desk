-- Enabled on first database creation (ADR-0012, ADR-0030).
-- pgvector: semantic retrieval embeddings. pg_search: BM25 keyword retrieval (ParadeDB).
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_search;
