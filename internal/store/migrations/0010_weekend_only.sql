UPDATE models AS m
SET price_rules = converted.rules
FROM (
    SELECT m.id,
           jsonb_agg(
               CASE
                   WHEN rule.value->>'days' = 'non_workday' THEN
                       jsonb_set(
                           jsonb_set(rule.value, '{days}', '"weekend"'::jsonb),
                           '{name}',
                           to_jsonb(replace(coalesce(rule.value->>'name', ''), '周末和节假日', '周末'))
                       )
                   ELSE rule.value
               END
               ORDER BY rule.ordinality
           ) AS rules
    FROM models AS m
    CROSS JOIN LATERAL jsonb_array_elements(m.price_rules) WITH ORDINALITY AS rule(value, ordinality)
    WHERE rule.value->>'days' = 'non_workday'
    GROUP BY m.id
) AS converted
WHERE m.id = converted.id;

DELETE FROM settings WHERE key = 'cn_holidays';
