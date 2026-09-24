import { Alert, Card, Col, Divider, Flex, Row, Table, Tag, Typography, theme } from 'antd';
import type { ReactNode } from 'react';
import { COMPOSE_STATE_META, CODE_FONT_STACK } from '../app/theme';
import { MetricChart, summarizeMetric } from '../components/MetricChart';
import type { MetricPoint } from '../components/MetricChart';
import { ComposeStatusTag, PageHeader } from '../components/primitives';

const { Text, Title, Paragraph, Link } = Typography;

/**
 * A short, deterministic stand-in for the Monitoring screen's own data — the
 * guideline renders the two states a reviewer would otherwise have to imagine.
 */
const PARTIAL_DEMO: MetricPoint[] = Array.from({ length: 26 }, (_, index) => ({
  at: Date.now() - (25 - index) * 60_000,
  value: 38 + 14 * Math.sin(index / 3) + (index % 5 === 0 ? 9 : 0),
}));

/**
 * A container CPU that spends most of its life above one core: the reading the
 * host chart's fixed 0–100 axis would clip.
 */
const WORKER_DEMO: MetricPoint[] = Array.from({ length: 40 }, (_, index) => ({
  at: Date.now() - (39 - index) * 60_000,
  value: Math.round((5 + 145 * ((index % 20) / 20)) * 10) / 10,
}));

const DEPLOY_DEMO = [{ at: Date.now() - 24 * 60_000, label: 'Deployed' }];

const NO_DATA_SUMMARY = { avg: 0, current: 0, max: 0, min: 0 };

function Section({ children, id, intro, title }: { children: ReactNode; id: string; intro?: ReactNode; title: string }) {
  return (
    <Card id={id} style={{ marginBottom: 16 }} title={title} variant="outlined">
      {intro && <Paragraph type="secondary">{intro}</Paragraph>}
      {children}
    </Card>
  );
}

function Rule({ children }: { children: ReactNode }) {
  return (
    <li style={{ marginBottom: 6 }}>
      <Text>{children}</Text>
    </li>
  );
}

/**
 * The guideline itself, rendered inside the prototype so the rules and the
 * screens they describe can be reviewed side by side.
 */
export function GuidelinesPage() {
  const { token } = theme.useToken();

  return (
    <>
      <PageHeader
        description="Why the screens look the way they do, what to reuse, and what the backend has to grow to support them."
        tags={<Tag color="processing">v1 proposal</Tag>}
        title="Design guidelines"
      />

      <Alert
        message="This app is a design prototype"
        description="Every screen runs on mock data in src/data/mocks.ts. Nothing calls the API. Treat the layouts, states and copy as the deliverable — the wiring is a separate pass."
        showIcon
        style={{ marginBottom: 16 }}
        type="warning"
      />

      <Section
        id="changes"
        intro="The redesign keeps the domain model exactly as it is (Project → Environment → Compose, CONTEXT.md) and the antd v6 default tokens (DESIGN.md). What changes is the information architecture and the amount of state the UI admits to."
        title="What changed, screen by screen"
      >
        <Table
          columns={[
            { dataIndex: 'screen', key: 'screen', title: 'Screen', width: 160 },
            { dataIndex: 'before', key: 'before', title: 'Before' },
            { dataIndex: 'after', key: 'after', title: 'After' },
            { dataIndex: 'why', key: 'why', title: 'Why' },
          ]}
          dataSource={[
            {
              key: 1,
              screen: 'Landing',
              before: 'A Projects list, nothing else.',
              after: 'Overview: host totals, a “needs attention” list, recent activity, and a flat All Composes table.',
              why: 'The operator opens the panel to answer “is anything broken?”, not “what exists?”.',
            },
            {
              key: 2,
              screen: 'Project',
              before: 'Environments as Tabs, with Overview / Variables / Composes as Tabs nested inside them.',
              after: 'Environment becomes a Segmented switcher; Composes / Variables / Settings are the only tabs.',
              why: 'Nested tabs hide depth and make the active Environment hard to keep in mind. One tab level, always visible.',
            },
            {
              key: 3,
              screen: 'Compose',
              before: 'Back button, five same-weight action buttons, 480px editor, Logs in a second tab.',
              after: 'Full-height editor + a status rail (services, deploy identity, Environment, activity); Logs in a drawer; a full screen editor with the Environment variables beside the file.',
              why: 'Actions get weight by importance, and “what is running / where does it live” is answered without leaving the screen.',
            },
            {
              key: 4,
              screen: 'Environment Variables',
              before: 'Table of plain-text values inside a nested tab.',
              after: 'Masked values with reveal/copy, key filter, and a paste-a-.env bulk editor with a preview.',
              why: 'These are secrets typed in from files the operator already has. Masking and bulk entry match the real job.',
            },
            {
              key: 5,
              screen: 'Destructive actions',
              before: 'Popconfirm on every Delete.',
              after: 'Popconfirm for reversible-enough things; type-the-name modal for Project, Environment and Compose deletes.',
              why: 'Deleting a Compose removes containers, networks and volumes on the host. The friction is the feature.',
            },
            {
              key: 6,
              screen: 'Auth',
              before: 'Bare card on a grey page.',
              after: 'Split layout: what the panel is on the left, the form on the right; setup is a two-step flow with a success step.',
              why: 'First run is the only chance to explain the mental model. A bare card reads like an error page.',
            },
            {
              key: 7,
              screen: 'Global',
              before: 'No way to jump; status only refreshed per page.',
              after: '⌘K palette over Projects/Composes, header refresh-all with an attention badge, breadcrumbs on every screen.',
              why: 'Deep nesting is fine when you can jump and always know where you are.',
            },
            {
              key: 8,
              screen: 'Monitoring',
              before: 'Nothing — the panel had no host view at all.',
              after: 'Six host charts over a 15m / 1h / 6h / 24h window, with the sampling budget stated on the screen.',
              why: '“Is the host in trouble?” is the other question the panel exists to answer, and it must not answer it with the container’s own counters.',
            },
            {
              key: 9,
              screen: 'Compose · Monitor',
              before: 'Nothing — how one service had been behaving could only be guessed from its logs.',
              after: 'One service of the Compose over the same four windows, on the same chart, with a marker on the last deploy.',
              why: '“What did that deploy do to this service?” is the question an operator asks right after deploying, and a chart keyed by container could not answer it.',
            },
          ]}
          pagination={false}
          size="small"
        />
      </Section>

      <Section
        id="principles"
        intro="antd's four values, applied to this panel. When two options conflict, the one that leaves the operator more certain wins."
        title="Principles"
      >
        <Row gutter={[16, 16]}>
          {[
            {
              body: 'Navigation mirrors the domain and nothing else: Sider = sections, breadcrumb = where you are, tabs = sections of one Environment. No invented groupings.',
              title: 'Natural',
            },
            {
              body: 'Every screen states its own status: unsaved changes, last checked time, “not deployed yet”. Pending actions show spinners on the control that started them.',
              title: 'Certain',
            },
            {
              body: 'Primary buttons are reserved for Deploy, Create and Save. Status colours carry meaning; decorative colour is not used.',
              title: 'Meaningful',
            },
            {
              body: 'Same header, same status tag, same empty-state shape, from a 4-Compose project to a 40-Compose host. Density comes from tables, not from shrinking the grammar.',
              title: 'Growing',
            },
          ].map((principle) => (
            <Col key={principle.title} md={12} xs={24}>
              <Card size="small" style={{ height: '100%' }} variant="outlined">
                <Title level={5}>{principle.title}</Title>
                <Text type="secondary">{principle.body}</Text>
              </Card>
            </Col>
          ))}
        </Row>
      </Section>

      <Section id="navigation" intro="Three levels, one switcher, no nesting." title="Navigation model">
        <pre
          style={{
            background: token.colorFillQuaternary,
            borderRadius: token.borderRadius,
            fontFamily: CODE_FONT_STACK,
            fontSize: 12,
            margin: 0,
            overflowX: 'auto',
            padding: 16,
          }}
        >{`Sider (sections)          Breadcrumb (context)                          Content
─────────────────────────────────────────────────────────────────────────────────────────
Overview                 Overview                                      dashboard
Projects                 Overview / Projects                            cards | table
  └ my-saas              Overview / Projects / my-saas                  [environment switcher]
                                 └ Composes | Variables | Settings
  └ landing              … / my-saas / production / web                 editor + status rail
Design system            Design system / Guidelines                     this page

URLs (hash, mock):  #/projects/:projectId/env/:environmentId/:tab
                    #/projects/:projectId/env/:environmentId/compose/:composeId
                    #/projects/:projectId/env/:environmentId/compose/:composeId/monitor
                    #/design/guidelines | #/design/states | #/signin | #/setup`}</pre>
      </Section>

      <Section
        id="status"
        intro="One vocabulary, used everywhere a Compose state is shown. Derived from the API's running/total counts — no new backend field needed."
        title="Status vocabulary"
      >
        <Table
          columns={[
            {
              dataIndex: 'state',
              key: 'state',
              render: (state) => <ComposeStatusTag lastDeployedAt="2026-01-01T00:00:00Z" status={state} />,
              title: 'Tag',
              width: 220,
            },
            { dataIndex: 'label', key: 'label', title: 'Label', width: 160 },
            { dataIndex: 'condition', key: 'condition', title: 'When' },
            { dataIndex: 'use', key: 'use', title: 'Where it is used' },
          ]}
          dataSource={[
            {
              condition: 'running = total > 0',
              key: 'running',
              label: COMPOSE_STATE_META.running.label,
              state: { running: 3, total: 3 },
              use: 'Headers, cards, table cells',
            },
            {
              condition: '0 < running < total',
              key: 'partial',
              label: COMPOSE_STATE_META.partial.label,
              state: { running: 1, total: 2 },
              use: 'Raises the Overview “needs attention” list',
            },
            {
              condition: 'running = 0 and previously deployed',
              key: 'stopped',
              label: COMPOSE_STATE_META.stopped.label,
              state: { running: 0, total: 2 },
              use: 'Stopped deliberately vs. never deployed is a real distinction',
            },
            {
              condition: 'total = 0, never deployed',
              key: 'not-deployed',
              label: COMPOSE_STATE_META['not-deployed'].label,
              state: { running: 0, total: 0 },
              use: 'Dashed outline so it never reads as “healthy but empty”',
            },
          ]}
          pagination={false}
          size="small"
        />
        <Divider />
        <Flex gap={8} wrap>
          <Text type="secondary">Service-level states in the status rail:</Text>
          <Tag color="success" variant="filled">
            running
          </Tag>
          <Tag color="error" variant="filled">
            exited
          </Tag>
          <Tag color="warning" variant="filled">
            restarting
          </Tag>
          <Tag variant="outlined">created</Tag>
        </Flex>
        <Text type="secondary" style={{ display: 'block', marginTop: 8 }}>
          Never colour a whole row by status — colour the dot, the tag or the count. Rows carry data, not mood.
        </Text>
      </Section>

      <Section id="layout" title="Layout and spacing grammar">
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>
            Two bars: a 56px header (<Text code>Layout.headerHeight</Text>) and a 248px sider. Content is a 24px-padded, 1280px-max column.
          </Rule>
          <Rule>All gaps come from the antd spacing scale (8 / 16 / 24). No magic numbers.</Rule>
          <Rule>
            The header&rsquo;s right cluster (search, refresh, theme, account) is pushed out by <Text code>marginInlineStart: &lsquo;auto&rsquo;</Text>, not by <Text code>justify=&quot;space-between&quot;</Text> alone. The breadcrumb is hidden on routes with a single crumb, so the cluster would be that Flex&rsquo;s only child — which <Text code>space-between</Text> parks at the start. It must land on the 24px page gutter.
          </Rule>
          <Rule>Surfaces: page = <Text code>colorBgLayout</Text>, cards = <Text code>colorBgContainer</Text>, everything inside a card is a token away — never a hex.</Rule>
          <Rule>One <Text code>PageHeader</Text> per screen: breadcrumb (from the shell), title + tags, secondary description, actions on the right. It always ends with a hairline border.</Rule>
          <Rule>Cards are for grouping, not decoration: if a card holds one line of text, it should not be a card.</Rule>
          <Rule>
            Editor screens are the exception to the max-width rule: they use the full 1280px column, and the editor stretches to match the status rail beside it — <Text code>LAYOUT.editorMinHeight</Text> (<Text code>clamp(640px, 80vh, 1000px)</Text>) is only the floor.
          </Rule>
          <Rule>
            No nested scrollers. Pages scroll with the document; the only bounded boxes are the editor and the logs viewer, where a bounded box <em>is</em> the feature. In the full screen editor the modal is that bounded box, and its one internal scroller is the 340px variables column beside the file.
          </Rule>
          <Rule>Tables over card grids once a list passes ~12 items; the Projects screen ships both and lets the operator choose.</Rule>
        </ul>
      </Section>

      <Section id="patterns" title="Component patterns to reuse">
        <Row gutter={[16, 16]}>
          {[
            { body: 'Title + tags + secondary description + right-aligned actions, hairline bottom border.', name: 'PageHeader' },
            { body: 'Label, big number, 12px hint underneath, optional 32px icon chip. Four per row on desktop, two on tablet.', name: 'StatCard' },
            { body: 'Dashed tag for “not deployed”, filled tag with counts otherwise. Dot form inside tables.', name: 'ComposeStatusTag / Inline' },
            { body: 'Circle glyph, title, one-line explanation, and the action that resolves it. Never a bare “No data”.', name: 'EmptyState' },
            { body: 'Type-the-name modal with a list of what will be destroyed, plus a warning banner. Popconfirm only for cheap deletes.', name: 'DangerModal' },
            { body: 'Monaco on the left, status rail on the right, unsaved-changes banner inside the editor, one primary Deploy button.', name: 'Compose editor' },
            { body: 'Full screen modal over the same file (one shared Monaco model, so closing never strands an edit), toolbar with Save/Discard/Close, and a variables rail where a row or Insert drops `${KEY}` at the cursor — the reference, never the value.', name: 'Full screen editor' },
            { body: 'Drawer, not a tab: service filter, line count, copy. “Follow” is visible but disabled — honesty about v1 scope.', name: 'Logs' },
            { body: '⌘K modal over Projects and Composes. The domain is deep; typing beats walking.', name: 'Command palette' },
            { body: 'Fixed-height inline SVG, no chart library: 0–100 axis for percent, nice-max for byte rates, hairline grid, crosshair tooltip, and min/max downsampling to the plot width. `axis="auto"` takes the maximum from the data for a container that can exceed one core; `markers` draws dashed vertical references such as a deploy. “Collecting” and “not readable” are states, not errors.', name: 'MetricChart' },
            { body: 'The Compose screen’s Monitor tab: one service over the four windows, a facts strip that says why history is keyed by service rather than by container, and a deploy marker on all four charts.', name: 'ServiceMonitor' },
          ].map((pattern) => (
            <Col key={pattern.name} md={12} xs={24}>
              <Card size="small" style={{ height: '100%' }} variant="outlined">
                <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                  {pattern.name}
                </Text>
                <Paragraph style={{ marginBottom: 0, marginTop: 4 }} type="secondary">
                  {pattern.body}
                </Paragraph>
              </Card>
            </Col>
          ))}
        </Row>
      </Section>

      <Section
        id="monitoring"
        intro="The panel reads host metrics itself — no agent, no sidecar — so the screen also has to be honest about what it cannot read."
        title="Monitoring and host metrics"
      >
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>
            Never plot more points than the plot has horizontal pixels. Each bucket keeps its min <em>and</em> its max, so a
            one-sample spike survives the trip to the screen — otherwise the interesting minute of a 24-hour window is drawn
            between two columns and vanishes.
          </Rule>
          <Rule>
            Two states are part of the contract, not error handling: <Text code>partial</Text> (“collecting — 187 of 360
            minutes”, plotting what exists after a restart) and <Text code>unavailable</Text> (the source is not readable —
            an <Text code>EmptyState</Text>, never a zeroed line).
          </Rule>
          <Rule>
            Never render container-scoped numbers as host numbers. The container’s <Text code>/proc/net/dev</Text> shows the
            same interface names as the host with its own counters; if the panel is reading its own namespace, the chart
            says so instead of drawing a plausible line. The working form is <Text code>/host/proc/1/net/dev</Text> — PID
            1’s network namespace is the host’s.
          </Rule>
          <Rule>
            Percent metrics keep a fixed 0–100 axis; byte rates auto-scale to a nice maximum. A value at or above 85%
            switches the <em>current value</em> to <Text code>colorWarning</Text> — never the line, never the card.
          </Rule>
          <Rule>
            The page scrolls with the document; the charts are fixed-height boxes and nothing on the screen is a nested
            scroller.
          </Rule>
          <Rule>
            Sampling budget: <strong>10s</strong> inside the hour (360 samples in an in-process ring buffer), 1-minute rollups for the
            rest of the day, under 10 ms and ~7 KB per read. <Text code>docker system df</Text> costs 0.64–0.68 s, so it is
            cached and refreshed every few minutes. The ring is memory only and the rollups are rows in the panel&rsquo;s own
            SQLite, so a restart costs the current hour&rsquo;s detail, not the day. No resident metrics service: cAdvisor
            measured 25&rarr;33 MB RSS and climbing here, with <Text code>--privileged</Text> and five host mounts.
          </Rule>
        </ul>
        <Divider />
        <Row gutter={[16, 16]}>
          <Col md={12} xs={24}>
            <MetricChart
              collecting={{ haveMinutes: 26, ofMinutes: 60 }}
              series={[{ key: 'cpu', label: 'CPU', points: PARTIAL_DEMO }]}
              state="partial"
              summary={summarizeMetric(PARTIAL_DEMO)}
              title="CPU Usage"
              unit="percent"
            />
          </Col>
          <Col md={12} xs={24}>
            {/* Nothing was read, so there is nothing to pass but the shape. */}
            <MetricChart
              series={[]}
              state="unavailable"
              summary={NO_DATA_SUMMARY}
              title="Network I/O"
              unit="bytesPerSecond"
              unavailableReason="The panel is reading /proc/net/dev from its own namespace. Those counters belong to the panel container, not the host — mount host /proc and read /host/proc/1/net/dev."
            />
          </Col>
        </Row>
      </Section>

      <Section
        id="service-metrics"
        intro="Host metrics live on #/monitoring; one service of one Compose lives on the Compose screen’s Monitor tab (#/projects/:p/env/:e/compose/:c/monitor). Same chart, same windows, same control family — a different question."
        title="Per-service metrics (Compose → Monitor)"
      >
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>
            History is keyed by <Text code>(Compose, service)</Text>, never by container ID. A deploy mints a new container,
            so a history keyed by container would restart at zero on the very deploy the operator is trying to read. The
            facts strip says this out loud: the container ID changes on every deploy, the service name does not.
          </Rule>
          <Rule>
            Container CPU is <strong>not clamped at 100%</strong>: a container that uses two cores reads 150%, and that is a
            reading, not an error. The CPU chart takes its axis from the data (<Text code>axis=&quot;auto&quot;</Text>);
            the host chart keeps its fixed 0–100 axis.
          </Rule>
          <Rule>
            Memory is <strong>usage minus page cache</strong>, against the container’s own limit — page cache is the host’s
            memory rather than the service’s footprint, and subtracting it is what makes the number agree with{' '}
            <Text code>docker stats</Text>. The footer carries the absolute reading (“412 MB of 2 GB used”).
          </Rule>
          <Rule>
            The last deploy is a dashed vertical <Text code>Deployed</Text> marker on all four charts, drawn only when it
            falls inside the selected window — so a spike can be read against the deploy that caused it.
          </Rule>
          <Rule>
            States: a Compose that has never been deployed (or has no services) gets the <Text code>EmptyState</Text>{' '}
            pointing at the Editor tab, because deploying is what starts collection; a service with no history in the
            window renders <Text code>partial</Text>; a service that is not running now keeps the history it has.
          </Rule>
          <Rule>
            Per-container sampling is coarser than the host’s: <strong>30s</strong> inside the hour, 1-minute rollups
            beyond it, ~72 bytes a sample — ~170 KB for the hour across 20 containers and ~2 MB/day in the panel’s
            SQLite.
          </Rule>
        </ul>
        <Divider />
        <Row gutter={[16, 16]}>
          <Col md={12} xs={24}>
            <MetricChart
              axis="auto"
              markers={DEPLOY_DEMO}
              series={[{ key: 'cpu', label: 'CPU', points: WORKER_DEMO }]}
              summary={summarizeMetric(WORKER_DEMO)}
              title="CPU Usage"
              unit="percent"
            />
          </Col>
        </Row>
      </Section>

      <Section id="feedback" title="Feedback and async rules">
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>Loading belongs to the control that started it: button spinners for actions, skeletons for first paint, table <Text code>loading</Text> for refetches.</Rule>
          <Rule>
            Confirmations use <Text code>App.useApp()</Text> messages (one line, names the thing: “Deployed web”), never a modal. Modals are for decisions, messages for outcomes.
          </Rule>
          <Rule>Errors stay on the screen they belong to, as an <Text code>Alert</Text> with a Retry action — never a toast that vanishes before it is read.</Rule>
          <Rule>Deploy is optimistic about intent and honest about result: the status tag only changes after the mock/API call resolves.</Rule>
          <Rule>Unsaved YAML is always visible: an “Unsaved changes” tag in the header, a “Modified” marker in the editor toolbar, and a banner offering Save/Discard.</Rule>
          <Rule>Empty and error states are designed, not defaulted — see the States gallery for the full set.</Rule>
        </ul>
      </Section>

      <Section id="a11y" title="Accessibility and content">
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>
            Status is never colour-only: the tag text carries the meaning (“Partly running”), the colour reinforces it.
          </Rule>
          <Rule>Icon-only buttons carry <Text code>aria-label</Text> (refresh, reveal value, row actions).</Rule>
          <Rule>Keyboard: ⌘K opens search, Esc closes overlays, tables are reachable by tab with visible focus rings from antd.</Rule>
          <Rule>
            Contrast: the default <Text code>#1677FF</Text> primary on white is below WCAG AA for small text (DESIGN.md notes this). If strict AA is required, darken <Text code>colorPrimary</Text> in <Text code>ConfigProvider</Text> rather than restyling individual screens.
          </Rule>
          <Rule>
            Copy names the domain, not the machinery: “Compose”, “Environment”, “Deploy identity”. Never “stack”, “app”, “job”, “record”.
          </Rule>
          <Rule>Destructive copy says what disappears: containers, networks, volumes, files under a named path.</Rule>
        </ul>
      </Section>

      <Section
        id="api-gaps"
        intro="Design decisions that need something the Go API does not return yet. Each is optional — the screens degrade gracefully without it, but they are worth a ticket."
        title="What the API has to grow"
      >
        <Table
          columns={[
            { dataIndex: 'element', key: 'element', title: 'Design element', width: 240 },
            { dataIndex: 'today', key: 'today', title: 'API today' },
            { dataIndex: 'needed', key: 'needed', title: 'Needed' },
          ]}
          dataSource={[
            {
              element: 'Per-service rows in the status rail',
              key: 1,
              needed: 'A services array per Compose (name, image, state, health) — `docker compose ps --format json` already has it.',
              today: 'Only status.running / status.total.',
            },
            {
              element: '“Last checked” time',
              key: 2,
              needed: 'Return the timestamp of the status read, or let the client stamp it (current mock behaviour).',
              today: 'Status is computed per request with no timestamp.',
            },
            {
              element: 'Activity feed',
              key: 3,
              needed: 'An events table, or client-side only. v1 can ship without it.',
              today: 'Nothing recorded.',
            },
            {
              element: 'Secret marking on variables',
              key: 4,
              needed: 'A boolean per variable (or a name heuristic) so masking is deliberate rather than guessed.',
              today: 'Values are returned in plain text with no flag.',
            },
            {
              element: 'Attention count in the header',
              key: 5,
              needed: 'Nothing — derived client-side from the existing composes list.',
              today: 'Already derivable.',
            },
            {
              element: 'Log service filter',
              key: 6,
              needed: 'Optional `?service=` pass-through to `docker compose logs`.',
              today: 'Returns the last ~200 lines for the whole stack; the mock filters client-side.',
            },
            {
              element: 'Network I/O counters',
              key: 7,
              needed: 'A read-only bind-mount of host `/proc` at `/host/proc`, and network counters read from `/host/proc/1/net/dev` — PID 1’s netns is the host’s. `/host/proc/net/dev` is not enough: it is `/proc/self/net/dev`.',
              today: 'The container’s own `/proc/net/dev` shows the same interface names with container-scoped counters (measured: 200 B rx inside vs 5.27 GB rx on the host). Until it is wired, the chart renders `unavailable`.',
            },
            {
              element: 'Docker disk usage',
              key: 8,
              needed: 'Cache `docker system df` and refresh it every few minutes; serve the chart from that cache.',
              today: 'Nothing — and it is too expensive for the 10s tick: 0.64–0.68 s and ~32 MB of transient RSS per call. `docker stats --no-stream` costs 2.03 s.',
            },
            {
              element: 'Metric history across restarts',
              key: 9,
              needed: 'Persist the 1-minute rollups in the panel’s existing SQLite so the day survives a restart; the 10s ring stays in memory, and “collecting — N of M minutes” covers the seam.',
              today: 'Nothing — nothing is persisted yet.',
            },
            {
              element: 'CPU, memory, block I/O, disk space',
              key: 10,
              needed: 'No API change — already host-accurate from inside the container (`/proc/stat`, `/proc/meminfo`, `/proc/diskstats`, `statfs` on the data dir). Do not build on memory staying host-global: that is Docker runtime behaviour, not a contract.',
              today: 'Readable today (measured).',
            },
          ]}
          pagination={false}
          size="small"
        />
      </Section>

      <Section id="implement" title="Implementing this in web/">
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <Rule>
            Keep the stack as-is: Vite + React + antd v6 + Tailwind, <Text code>StyleProvider layer</Text> + <Text code>ConfigProvider</Text>. The prototype uses the same antd version (6.5.1) so tokens, APIs and class names line up.
          </Rule>
          <Rule>
            Port <Text code>src/app/theme.ts</Text> first — the component-token overrides (Layout, Menu, Table, Card) are what make every screen feel consistent.
          </Rule>
          <Rule>
            The mock store in <Text code>src/data/store.tsx</Text> is shaped like a data layer: swap its bodies for <Text code>fetch</Text> calls to the existing endpoints and the components do not change. The one place the mock differs from the API is where the API returns nested data (composes under an environment) — resolve that with parallel requests as today.
          </Rule>
          <Rule>
            Pick a router when implementing: the prototype's hash router exists only so every screen is linkable. Any real router must keep the paths in <Text code>src/app/router.ts</Text>.
          </Rule>
          <Rule>
            Do not port the prototype strip or the latency switch. They exist to review loading states.
          </Rule>
          <Rule>
            Dark mode is wired through <Text code>ConfigProvider</Text> in the prototype to prove the tokens hold up; ADR-0004 defers it for v1. For antd it is a one-line algorithm swap — but the editor themes itself, so Monaco needs its own theme pair (<Text code>monacoThemeData</Text> in <Text code>app/theme.ts</Text>) fed from the same tokens.
          </Rule>
        </ul>
        <Divider />
        <Text type="secondary">
          Screens: <Link href="#/">Overview</Link> · <Link href="#/monitoring">Monitoring</Link> ·{' '}
          <Link href="#/projects">Projects</Link> · <Link href="#/projects/1">Project</Link> ·{' '}
          <Link href="#/projects/1/env/1/compose/1">Compose</Link> ·{' '}
          <Link href="#/projects/1/env/1/compose/1/monitor">Compose Monitor</Link> ·{' '}
          <Link href="#/design/states">States gallery</Link> · <Link href="#/signin">Sign in</Link> ·{' '}
          <Link href="#/setup">Setup</Link>
        </Text>
      </Section>
    </>
  );
}
