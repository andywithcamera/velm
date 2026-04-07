UPDATE _app
SET definition_yaml = trim($yaml$
name: itsm
namespace: itsm
label: ITSM
description: Incident, change, request, CMDB, and operational support application
tables:
  - name: itsm_incident
    label_singular: Incident
    label_plural: Incidents
    description: Incident records for the ITSM application.
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: true
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: text
        is_nullable: true
      - name: status
        label: Status
        data_type: text
        is_nullable: false
      - name: priority
        label: Priority
        data_type: text
        is_nullable: false
  - name: itsm_problem
    label_singular: Problem
    label_plural: Problems
    description: Problem records for the ITSM application.
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: true
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: text
        is_nullable: true
      - name: status
        label: Status
        data_type: text
        is_nullable: false
      - name: priority
        label: Priority
        data_type: text
        is_nullable: false
  - name: itsm_change_request
    label_singular: Change Request
    label_plural: Change Requests
    description: Change records for the ITSM application.
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: true
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: text
        is_nullable: true
      - name: status
        label: Status
        data_type: text
        is_nullable: false
      - name: priority
        label: Priority
        data_type: text
        is_nullable: false
  - name: itsm_service_request
    label_singular: Service Request
    label_plural: Service Requests
    description: Request records for the ITSM application.
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: true
      - name: title
        label: Title
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: text
        is_nullable: true
      - name: status
        label: Status
        data_type: text
        is_nullable: false
      - name: priority
        label: Priority
        data_type: text
        is_nullable: false
  - name: itsm_cmdb_ci
    label_singular: Configuration Item
    label_plural: Configuration Items
    description: Configuration items for the ITSM application.
    columns:
      - name: number
        label: Number
        data_type: text
        is_nullable: true
      - name: name
        label: Name
        data_type: text
        is_nullable: false
      - name: description
        label: Description
        data_type: text
        is_nullable: true
pages:
  - name: Incident Ops
    slug: incident-ops
    editor_mode: wysiwyg
    status: draft
    content: |
      <section><h1>Incident Ops</h1><p>Seeded from YAML.</p></section>
seeds:
  - table: _page
    rows:
      - name: Incident Ops
        slug: incident-ops
        editor_mode: wysiwyg
        status: draft
        content: "<section><h1>Incident Ops</h1><p>Seeded from YAML.</p></section>"
$yaml$)
WHERE namespace = 'itsm';
