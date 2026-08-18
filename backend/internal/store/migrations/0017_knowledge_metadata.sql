-- 0017 — Full metadata for the knowledge index (FR-M4-05) on the SAME table the
-- retrieve path reads (knowledge_items, ADR-0012/0015). Chunk text stays in
-- `content`; every retrieval/isolation/freshness predicate of SR-M4-01 gets a
-- column so the persisted rows reconstruct a knowledge.Item exactly.
--
-- knowledge_items is MUTABLE by design (items are retired/edited — FR-M4-11), so
-- no INV-2 immutability trigger. RLS/grants are inherited from 0001. Idempotent.

ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS brand_id       uuid REFERENCES brands(id) ON DELETE SET NULL;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS language       text;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS url            text;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS source         text;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS owner          text;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS authority_tier int  NOT NULL DEFAULT 4;   -- Website
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS status         text NOT NULL DEFAULT 'active';
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS last_verified  timestamptz;
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS review_ttl_seconds bigint;                -- 0/null = no TTL
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS valid_from     timestamptz;               -- null = unbounded
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS valid_until    timestamptz;               -- null = unbounded
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS chunk_seq      int  NOT NULL DEFAULT 0;
-- ponytail: the semantic vector is stored as real[] to avoid a pgvector-go binding
-- for this write slice; the `vector` extension is present (store.CheckExtensions)
-- and the upgrade path is a `vector` column + ANN index once SQL-side hybrid
-- ranking lands (ADR-0012).
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS embedding      real[];
ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS created_at     timestamptz NOT NULL DEFAULT now();

-- Isolation/filter helper: tenant+brand scoping is a structural predicate (SR-M4-01).
CREATE INDEX IF NOT EXISTS knowledge_items_scope_idx ON knowledge_items (tenant_id, brand_id);
