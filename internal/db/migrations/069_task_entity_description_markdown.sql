-- Change base_task.description and base_entity.description from long_text to markdown
-- so the form renders them as a markdown editor with [[wikilink]] support.
-- Patterns are anchored to the column that follows each description column so that
-- base_task_state.description (followed by "category") is left unchanged.
UPDATE _app
SET
    definition_yaml = regexp_replace(
        regexp_replace(
            definition_yaml,
            '(- name: description\s+label: Description\s+data_type:) long_text(\s+is_nullable: true\s+- name: work_type)',
            E'\\1 markdown\\2',
            'g'
        ),
        '(- name: description\s+label: Description\s+data_type:) long_text(\s+is_nullable: true\s+- name: entity_type)',
        E'\\1 markdown\\2',
        'g'
    ),
    published_definition_yaml = regexp_replace(
        regexp_replace(
            published_definition_yaml,
            '(- name: description\s+label: Description\s+data_type:) long_text(\s+is_nullable: true\s+- name: work_type)',
            E'\\1 markdown\\2',
            'g'
        ),
        '(- name: description\s+label: Description\s+data_type:) long_text(\s+is_nullable: true\s+- name: entity_type)',
        E'\\1 markdown\\2',
        'g'
    ),
    definition_version    = definition_version    + 1,
    published_version     = published_version     + 1,
    _updated_at           = NOW()
WHERE name = 'base';
