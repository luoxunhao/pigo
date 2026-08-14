UPDATE sessions
SET metadata = json_remove(metadata, '$.customMetadata.thinkingLevel')
WHERE json_extract(metadata, '$.customMetadata.thinkingLevel') IS NOT NULL;

UPDATE sessions
SET metadata = json_remove(metadata, '$.header.laneConfig')
WHERE json_extract(metadata, '$.header.laneConfig') IS NOT NULL;
