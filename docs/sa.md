# System Analysis — Relist Panel

This document translates the decisions already recorded in `CONTEXT.md`, `docs/adr/*`, and `.scratch/compose-panel/spec.md` into standard SA diagrams (Context/DFD, Use Case, ER, Architecture, Sequence) and a Data Dictionary. It's a derived reference, not a new source of truth — if anything here ever disagrees with those documents, they win; update this file to match.

## 1. Context Diagram (DFD Level 0)

```mermaid
flowchart LR
    OP[Operator]
    DE[Docker Engine]
    TR[Traefik]
    SYS((Relist Panel))

    OP -->|"HTTP: create / deploy / manage"| SYS
    SYS -->|"responses, status, logs"| OP
    SYS -->|"docker compose commands via mounted socket"| DE
    DE -->|"container status, logs"| SYS
    SYS -.->|"ensures shared network exists"| TR
    TR -->|"routes requests to"| DE
```

## 2. Data Flow Diagram — Level 1

```mermaid
flowchart TD
    OP[Operator]
    P1((P1: Manage Project))
    P2((P2: Manage Environment))
    P3((P3: Manage Compose))
    P4((P4: Deploy Orchestration))
    D1[(D1: SQLite metadata)]
    D2[(D2: Compose files on disk)]
    DE[Docker Engine]

    OP --> P1
    OP --> P2
    OP --> P3
    OP --> P4
    P1 <--> D1
    P2 <--> D1
    P3 <--> D1
    P3 --> D2
    P4 <--> D1
    P4 <--> D2
    P4 -->|"up / down / restart / logs"| DE
    DE -->|"status, logs"| P4
    P4 -->|"results"| OP
```

## 3. Use Case Diagram

Single actor for v1 — no multi-user/team support (ADR-0001).

```mermaid
flowchart LR
    Operator(["👤 Operator"])
    subgraph System["Relist Panel"]
        UC1(["Setup Admin Account"])
        UC2(["Login"])
        UC3(["Create Project"])
        UC4(["Edit / Delete Project"])
        UC5(["Create Environment"])
        UC6(["Manage Environment Variables"])
        UC7(["Delete Environment"])
        UC8(["Create / Edit Compose"])
        UC9(["Deploy Compose"])
        UC10(["Stop Compose"])
        UC11(["Restart Compose"])
        UC12(["Delete Compose"])
        UC13(["View Compose Status"])
        UC14(["View Compose Logs"])
    end
    Operator --- UC1
    Operator --- UC2
    Operator --- UC3
    Operator --- UC4
    Operator --- UC5
    Operator --- UC6
    Operator --- UC7
    Operator --- UC8
    Operator --- UC9
    Operator --- UC10
    Operator --- UC11
    Operator --- UC12
    Operator --- UC13
    Operator --- UC14
```

### Use case summary

Full flows are in the spec's User Stories (`.scratch/compose-panel/spec.md`) — this table is a condensed index, not a duplicate.

| # | Use Case | Precondition | Postcondition | Spec user stories |
|---|---|---|---|---|
| UC1 | Setup Admin Account | No admin account exists yet | Admin account created | 5 |
| UC2 | Login | Admin account exists | Session established | 6 |
| UC3 | Create Project | Logged in | New Project exists | 7 |
| UC4 | Edit / Delete Project | Project exists | Project updated, or removed with its Environments/Composes torn down | 9, 10 |
| UC5 | Create Environment | Project exists | New Environment exists under the Project | 11, 12 |
| UC6 | Manage Environment Variables | Environment exists | Variable set added/edited/removed | 14, 15 |
| UC7 | Delete Environment | Environment exists | Environment removed, its Composes torn down | 16 |
| UC8 | Create / Edit Compose | Environment exists | Compose YAML saved | 17–20 |
| UC9 | Deploy Compose | Compose exists with YAML | Containers running, network joined, status updated | 22–25, 29 |
| UC10 | Stop Compose | Compose is deployed | Containers stopped, YAML retained | 26 |
| UC11 | Restart Compose | Compose is deployed | Containers cycled | 27 |
| UC12 | Delete Compose | Compose exists | Containers/network/volumes removed, files deleted | 28 |
| UC13 | View Compose Status | Compose exists | Current running/stopped state shown | 21, 30 |
| UC14 | View Compose Logs | Compose is/was deployed | Recent log lines shown | 31 |

## 4. Entity-Relationship Diagram

```mermaid
erDiagram
    PROJECT ||--o{ ENVIRONMENT : has
    ENVIRONMENT ||--o{ ENVIRONMENT_VARIABLE : has
    ENVIRONMENT ||--o{ COMPOSE : has

    PROJECT {
        int id PK
        string name
        string description
        datetime created_at
        datetime updated_at
    }
    ENVIRONMENT {
        int id PK
        int project_id FK
        string name
        datetime created_at
    }
    ENVIRONMENT_VARIABLE {
        string id PK
        string environment_id FK
        string key
        string value
    }
    COMPOSE {
        string id PK
        string environment_id FK
        string name
        text yaml_content
        datetime created_at
        datetime updated_at
    }
```

## 5. System Architecture Diagram

```mermaid
flowchart TB
    subgraph Host["Docker Host"]
        subgraph PanelContainer["relist-panel container"]
            API["Go HTTP API"]
            WEB["Embedded React SPA (go:embed)"]
            RUNNER["dockerexec.ComposeRunner"]
        end
        subgraph TraefikContainer["traefik container"]
            TR["Traefik"]
        end
        SOCK["/var/run/docker.sock"]
        DATA["/var/lib/relist-panel (data dir)"]
        DE["dockerd — Docker Engine"]
        NET(("relist-public network"))
        USERCONT["User-deployed containers\n(from Compose stacks)"]
    end

    Operator["Operator's browser"] -->|HTTPS, app domains| TR
    TR -->|routes by Docker labels| USERCONT
    Operator -->|HTTPS, admin UI| WEB
    WEB --> API
    API --> RUNNER
    RUNNER -->|"shell: docker compose ..."| SOCK
    SOCK --> DE
    DE -->|runs| USERCONT
    DE -->|runs| TraefikContainer
    API -->|reads/writes| DATA
    DATA -.->|identical host/container path bind mount| PanelContainer
    TraefikContainer --- NET
    USERCONT --- NET
```

## 6. Sequence Diagram — Deploy Compose

```mermaid
sequenceDiagram
    actor Op as Operator
    participant UI as React SPA
    participant API as Go API (compose package)
    participant Runner as dockerexec.ComposeRunner
    participant FS as Data directory (disk)
    participant CLI as docker compose CLI
    participant DE as Docker Engine

    Op->>UI: Click "Deploy"
    UI->>API: POST /composes/{id}/deploy
    API->>API: Load Compose YAML + Environment's variables
    API->>API: Inject relist-public network into every service
    API->>FS: Write resolved docker-compose.yml
    API->>FS: Write .env file with Environment Variables
    API->>Runner: Up(projectName, composeFilePath, envFilePath)
    Runner->>CLI: docker compose -p <name> -f <file> --env-file <env> up -d --build
    CLI->>DE: create/start containers, join network
    DE-->>CLI: containers running
    CLI-->>Runner: exit code 0
    Runner-->>API: success
    API-->>UI: 200 OK + status
    UI-->>Op: Show "Deployed" + status badge
```

## 7. Data Dictionary

Illustrative — the goose migration files (ADR-0003) are the authoritative schema once written; this is a planning reference.

### `projects`

| Field | Type | Constraint | Description |
|---|---|---|---|
| id | INTEGER | PK | Unique project identifier |
| name | TEXT | NOT NULL, UNIQUE | Project name |
| description | TEXT | NULL | Free-text description |
| created_at | DATETIME | NOT NULL | Creation timestamp |
| updated_at | DATETIME | NOT NULL | Last edit timestamp |

### `environments`

| Field | Type | Constraint | Description |
|---|---|---|---|
| id | INTEGER | PK | Unique environment identifier |
| project_id | INTEGER | FK → `projects.id`, NOT NULL | Owning project |
| name | TEXT | NOT NULL | e.g. "production" |
| created_at | DATETIME | NOT NULL | Creation timestamp |

Unique constraint: (`project_id`, `name`)

### `environment_variables`

| Field | Type | Constraint | Description |
|---|---|---|---|
| id | TEXT | PK | Unique identifier |
| environment_id | TEXT | FK → `environments.id`, NOT NULL | Owning environment |
| key | TEXT | NOT NULL | Variable name |
| value | TEXT | NOT NULL | Variable value |

Unique constraint: (`environment_id`, `key`)

### `composes`

| Field | Type | Constraint | Description |
|---|---|---|---|
| id | TEXT | PK | Unique identifier |
| environment_id | TEXT | FK → `environments.id`, NOT NULL | Owning environment |
| name | TEXT | NOT NULL | Compose name/slug |
| yaml_content | TEXT | NOT NULL | Raw `docker-compose.yml` content as saved |
| created_at | DATETIME | NOT NULL | Creation timestamp |
| updated_at | DATETIME | NOT NULL | Last edit timestamp |

Unique constraint: (`environment_id`, `name`)
