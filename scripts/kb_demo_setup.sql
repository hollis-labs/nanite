-- kb_demo_setup.sql
-- Sets up the kb_demo Postgres database for the support-ticket plugin.
-- Run with: psql --set ON_ERROR_STOP=1 -d kb_demo -f scripts/kb_demo_setup.sql

-- Required extension for trigram similarity fallback in kb_smart_search.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- kb_smart_search: hybrid full-text + trigram search over kb_articles.
-- Called by KBTransport.searchKB in the support-ticket plugin.
CREATE OR REPLACE FUNCTION kb_smart_search(
    p_query   text,
    p_category text DEFAULT NULL,
    p_tag      text DEFAULT NULL,
    p_limit    int  DEFAULT 5,
    p_source   text DEFAULT NULL
)
RETURNS TABLE (
    id           text,
    title        text,
    category     text,
    severity     text,
    tags         text[],
    related      text[],
    rank         float8,
    headline     text,
    match_method text
)
LANGUAGE plpgsql AS $$
DECLARE
    tsq tsquery;
BEGIN
    -- Build tsquery from the natural-language query.
    tsq := websearch_to_tsquery('english', p_query);

    RETURN QUERY
    WITH fts AS (
        -- Full-text search (exact match on tsquery).
        SELECT
            a.id, a.title, a.category, a.severity, a.tags, a.related,
            ts_rank_cd(to_tsvector('english', a.title || ' ' || a.body), tsq)::float8 AS rank,
            ts_headline('english', a.title || ' ' || a.body, tsq,
                        'StartSel=<<, StopSel=>>') AS headline,
            'fts_exact'::text AS match_method
        FROM kb_articles a
        WHERE to_tsvector('english', a.title || ' ' || a.body) @@ tsq
          AND (p_category IS NULL OR a.category = p_category)
          AND (p_tag IS NULL OR p_tag = ANY(a.tags))
          AND (p_source IS NULL OR a.source = p_source)
    ),
    fts_prefix AS (
        -- Full-text prefix search (broader match).
        SELECT
            a.id, a.title, a.category, a.severity, a.tags, a.related,
            ts_rank_cd(to_tsvector('english', a.title || ' ' || a.body),
                       to_tsquery('english', regexp_replace(p_query, '\s+', ':* & ', 'g') || ':*'))::float8 AS rank,
            ts_headline('english', a.title || ' ' || a.body, tsq,
                        'StartSel=<<, StopSel=>>') AS headline,
            'fts_prefix'::text AS match_method
        FROM kb_articles a
        WHERE to_tsvector('english', a.title || ' ' || a.body) @@
              to_tsquery('english', regexp_replace(p_query, '\s+', ':* & ', 'g') || ':*')
          AND (p_category IS NULL OR a.category = p_category)
          AND (p_tag IS NULL OR p_tag = ANY(a.tags))
          AND (p_source IS NULL OR a.source = p_source)
          AND a.id NOT IN (SELECT f.id FROM fts f)
    ),
    fuzzy AS (
        -- Trigram similarity fallback for typos / partial matches.
        SELECT
            a.id, a.title, a.category, a.severity, a.tags, a.related,
            similarity(a.title, p_query)::float8 AS rank,
            a.title AS headline,
            'fuzzy'::text AS match_method
        FROM kb_articles a
        WHERE similarity(a.title, p_query) > 0.15
          AND (p_category IS NULL OR a.category = p_category)
          AND (p_tag IS NULL OR p_tag = ANY(a.tags))
          AND (p_source IS NULL OR a.source = p_source)
          AND a.id NOT IN (SELECT f.id FROM fts f)
          AND a.id NOT IN (SELECT fp.id FROM fts_prefix fp)
    ),
    combined AS (
        SELECT * FROM fts
        UNION ALL
        SELECT * FROM fts_prefix
        UNION ALL
        SELECT * FROM fuzzy
    )
    SELECT c.id, c.title, c.category, c.severity, c.tags, c.related,
           c.rank, c.headline, c.match_method
    FROM combined c
    ORDER BY c.rank DESC
    LIMIT p_limit;
END;
$$;
