BEGIN;

UPDATE recipients SET status = 'cancelled' WHERE status = 'unsubscribed';
DROP TABLE unsubscribes;

COMMIT;
