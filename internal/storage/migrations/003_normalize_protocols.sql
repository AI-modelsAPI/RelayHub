-- 003_normalize_protocols.sql: normalize protocol names to design specification
UPDATE providers SET protocol = 'openai-chat' WHERE protocol = 'openai';
UPDATE providers SET protocol = 'anthropic-messages' WHERE protocol = 'anthropic';

UPDATE provider_models SET protocol = 'openai-chat' WHERE protocol = 'openai';
UPDATE provider_models SET protocol = 'anthropic-messages' WHERE protocol = 'anthropic';

UPDATE routes SET protocol = 'openai-chat' WHERE protocol = 'openai';
UPDATE routes SET protocol = 'anthropic-messages' WHERE protocol = 'anthropic';

UPDATE request_records SET protocol = 'openai-chat' WHERE protocol = 'openai';
UPDATE request_records SET protocol = 'anthropic-messages' WHERE protocol = 'anthropic';
