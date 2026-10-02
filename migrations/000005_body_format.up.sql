-- Plain-text campaigns and images embedded in the HTML body

BEGIN;

ALTER TABLE campaigns ADD COLUMN body_format varchar(8) NOT NULL DEFAULT 'html'
    CHECK (body_format IN ('html', 'text'));

-- set for images referenced from the body as <img src="cid:...">
ALTER TABLE attachments ADD COLUMN content_id text NOT NULL DEFAULT '';

COMMIT;
