import type { ComponentProps, ReactNode, CSSProperties } from "react"
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  McpService,
  type SerializedServerSummary,
} from "../bindings/github.com/VikashLoomba/mcp-client-manager-go/apps/mcp-manager-ui/index.js"
import { ConnectionStatus } from "../bindings/github.com/VikashLoomba/mcp-client-manager-go/pkg/mcpmgr/models.js"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table"
import { Separator } from "@/components/ui/separator"
import { Button } from "@/components/ui/button"
import { RefreshCcw } from "lucide-react"
import { cn } from "@/lib/utils"

type ServerSummary = SerializedServerSummary
type BadgeVariant = ComponentProps<typeof Badge>["variant"]
type GlassStyle = CSSProperties & { ["--glass-tint"]?: string; ["--chip-tint"]?: string }

const statusMeta: Record<string, { label: string; description: string; variant: BadgeVariant }> = {
  [ConnectionStatus.StatusConnected]: {
    label: "Connected",
    description: "Session established and responsive.",
    variant: "default",
  },
  [ConnectionStatus.StatusConnecting]: {
    label: "Connecting",
    description: "Handshake or reconnection in progress.",
    variant: "secondary",
  },
  [ConnectionStatus.StatusDisconnected]: {
    label: "Disconnected",
    description: "Server is registered but not currently reachable.",
    variant: "destructive",
  },
  unknown: {
    label: "Unknown",
    description: "Status is unavailable for this server.",
    variant: "secondary",
  },
}

const transportLabels: Record<string, string> = {
  stdio: "STDIO",
  http: "HTTP",
}

const formatTimeout = (seconds?: number) => {
  if (!seconds) {
    return "Uses manager default"
  }
  if (seconds < 60) {
    return `${seconds}s`
  }
  const mins = Math.floor(seconds / 60)
  const secs = seconds % 60
  return secs === 0 ? `${mins}m` : `${mins}m ${secs}s`
}

const formatBool = (value: boolean | null | undefined, fallback: string) => {
  if (value === undefined || value === null) {
    return fallback
  }
  return value ? "Yes" : "No"
}

const ServerCard = ({ summary }: { summary: ServerSummary }) => {
  const config = summary.config ?? undefined
  const statusInfo = statusMeta[summary.status] ?? statusMeta.unknown
  const transportLabel = config?.type ? transportLabels[config.type] ?? config.type.toUpperCase() : "Unknown"
  const statusTintMap: Record<string, string> = {
    [ConnectionStatus.StatusConnected]: "oklch(0.95 0.04 150 / 0.14)",
    [ConnectionStatus.StatusConnecting]: "oklch(0.96 0.025 85 / 0.12)",
    [ConnectionStatus.StatusDisconnected]: "oklch(0.95 0.045 30 / 0.14)",
    unknown: "oklch(0.96 0.012 260 / 0.1)",
  }
  const cardTint = statusTintMap[summary.status] ?? statusTintMap.unknown

  const args = Array.isArray(config?.args) ? config?.args ?? [] : []
  const envEntries =
    config?.env && typeof config.env === "object" ? Object.entries(config.env as Record<string, string>) : []

  const rows: Array<{ label: string; value: ReactNode }> = [
    {
      label: "Transport",
      value: (
        <Badge
          variant="glass"
          className="uppercase text-xs text-foreground/70"
          style={{ "--chip-tint": "oklch(0.96 0.012 260 / 0.1)" } as GlassStyle}
        >
          {transportLabel}
        </Badge>
      ),
    },
  ]

  if (!config) {
    rows.push({
      label: "Configuration",
      value: <span className="text-muted-foreground">Configuration details unavailable.</span>,
    })
  } else {
    rows.push(
      {
        label: "Timeout",
        value: formatTimeout(config.timeoutSeconds),
      },
      {
        label: "Version",
        value: config.version || "Inherit default",
      },
      {
        label: "Log JSON-RPC",
        value: (
          <Badge
            variant="glass"
            className={cn("px-2.5 py-1", config.logJsonRpc ? "text-emerald-900/70 dark:text-emerald-200" : "text-foreground/60")}
            style={{ "--chip-tint": config.logJsonRpc ? "oklch(0.95 0.035 150 / 0.12)" : "oklch(0.96 0.01 260 / 0.1)" } as GlassStyle}
          >
            {config.logJsonRpc ? "Enabled" : "Disabled"}
          </Badge>
        ),
      }
    )

    if (config.type === "stdio") {
      rows.push(
        {
          label: "Command",
          value: config.command ? <code className="rounded bg-muted px-2 py-1 text-xs">{config.command}</code> : "Not set",
        },
        {
          label: "Arguments",
          value: args.length ? (
            <div className="flex flex-wrap gap-2">
              {args.map((arg) => (
                <Badge
                  key={arg}
                  variant="glass"
                  className="font-mono text-[0.7rem]"
                  style={{ "--chip-tint": "oklch(0.96 0.012 260 / 0.1)" } as GlassStyle}
                >
                  {arg}
                </Badge>
              ))}
            </div>
          ) : (
            <span className="text-muted-foreground">None</span>
          ),
        }
      )
    }

    if (config.type === "http") {
      rows.push(
        {
          label: "Endpoint",
          value: config.endpoint ? (
            <span className="font-mono text-sm">{config.endpoint}</span>
          ) : (
            <span className="text-muted-foreground">Not configured</span>
          ),
        },
        {
          label: "Prefer SSE",
          value: formatBool(config.preferSse ?? undefined, "Auto"),
        },
        {
          label: "Max Retries",
          value: config.maxRetries !== undefined && config.maxRetries !== null ? config.maxRetries : "Auto",
        },
        {
          label: "Session ID",
          value: config.sessionId ? (
            <span className="font-mono text-xs">{config.sessionId}</span>
          ) : (
            <span className="text-muted-foreground">Not negotiated</span>
          ),
        }
      )
    }

    rows.push({
      label: "Environment",
      value: envEntries.length ? (
        <div className="flex flex-wrap gap-2">
          {envEntries.map(([key, value]) => (
            <Badge
              key={key}
              variant="glass"
              className="font-mono text-[0.7rem]"
              style={{ "--chip-tint": "oklch(0.96 0.012 260 / 0.1)" } as GlassStyle}
            >
              {key}={value}
            </Badge>
          ))}
        </div>
      ) : (
        <span className="text-muted-foreground">None</span>
      ),
    })
  }

  return (
    <Card className="h-full border-transparent glass-pressable" style={{ "--glass-tint": cardTint } as GlassStyle}>
      <CardHeader className="gap-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <CardTitle className="text-xl font-semibold">{summary.id}</CardTitle>
            <CardDescription>{statusInfo.description}</CardDescription>
          </div>
          <Badge
            variant="glass"
            className="px-3 py-1 text-foreground/80"
            style={{ "--chip-tint": cardTint } as GlassStyle}
          >
            {statusInfo.label}
          </Badge>
        </div>
        <Separator />
      </CardHeader>
      <CardContent className="space-y-6">
        <Table>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.label}>
                <TableCell className="w-36 whitespace-nowrap text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  <span className="glass-label">{row.label}</span>
                </TableCell>
                <TableCell className="align-top">{row.value}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}

const ServerDashboard = () => {
  const [servers, setServers] = useState<ServerSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)

  const fetchServers = useCallback(async () => {
    setLoading(true)
    try {
      const response = await McpService.GetServersWithDetails()
      setServers(response ?? [])
      setError(null)
      setLastUpdated(new Date())
    } catch (err) {
      console.error(err)
      setError(err instanceof Error ? err.message : "Failed to load server information.")
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchServers()
  }, [fetchServers])

  const stats = useMemo(() => {
    const totals = {
      total: servers.length,
      connected: servers.filter((s) => s.status === ConnectionStatus.StatusConnected).length,
      connecting: servers.filter((s) => s.status === ConnectionStatus.StatusConnecting).length,
      disconnected: servers.filter((s) => s.status === ConnectionStatus.StatusDisconnected || s.status === ConnectionStatus.$zero).length,
    }
    return [
      {
        id: "total",
        label: "Registered Servers",
        value: totals.total,
        description: "Known to the manager.",
        tint: "oklch(0.95 0.015 260 / 0.16)",
      },
      {
        id: "connected",
        label: "Active Connections",
        value: totals.connected,
        description: "Currently serving requests.",
        tint: "oklch(0.94 0.045 150 / 0.14)",
      },
      {
        id: "connecting",
        label: "Connecting",
        value: totals.connecting,
        description: "Negotiating or retrying sessions.",
        tint: "oklch(0.95 0.03 90 / 0.14)",
      },
      {
        id: "disconnected",
        label: "Disconnected",
        value: totals.disconnected,
        description: "Awaiting manual reconnection.",
        tint: "oklch(0.94 0.05 30 / 0.16)",
      },
    ]
  }, [servers])

  const refreshLabel = loading ? "Refreshing…" : "Refresh"

  return (
    <div className="min-h-screen from-background via-background to-accent/10 py-12">
      <main className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-6">
        <header className="flex flex-col gap-2">
          <h1 className="text-liquid text-3xl font-semibold tracking-tight">MCP Manager Dashboard</h1>
          <p className="text-muted-foreground">
            Inspect registered MCP servers, their transport configuration, and current connection health.
          </p>
        </header>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            onClick={() => void fetchServers()}
            disabled={loading}
            className="gap-2"
            variant="glass"
            style={{ "--glass-tint": "oklch(0.97 0.015 240 / 0.16)" } as GlassStyle}
          >
            <RefreshCcw className={cn("h-4 w-4", loading && "animate-spin")} />
            {refreshLabel}
          </Button>
          {lastUpdated && (
            <Badge
              variant="glass"
              className="text-xs font-medium text-foreground/70"
            style={{ "--chip-tint": "oklch(0.97 0.012 260 / 0.11)" } as GlassStyle}
            >
              Updated {lastUpdated.toLocaleTimeString()}
            </Badge>
          )}
        </div>
        {error && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm text-destructive shadow-sm">
            {error}
          </div>
        )}
        <section className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {stats.map((stat) => (
            <Card
              key={stat.id}
              className="border-transparent glass-pressable"
              style={{ "--glass-tint": stat.tint } as GlassStyle}
            >
              <CardHeader className="gap-1 pb-2">
                <CardDescription className="text-xs uppercase tracking-wide text-muted-foreground">
                  {stat.description}
                </CardDescription>
                <CardTitle className="text-2xl font-semibold">{stat.value}</CardTitle>
              </CardHeader>
              <CardContent className="pt-0">
                <p className="text-sm text-muted-foreground">{stat.label}</p>
              </CardContent>
            </Card>
          ))}
        </section>
        <section className="grid gap-6 md:grid-cols-2">
          {servers.map((server) => (
            <ServerCard key={server.id} summary={server} />
          ))}
        </section>
        {!loading && servers.length === 0 && !error && (
          <Card className="border-dashed border-border/60" style={{ "--glass-tint": "oklch(0.94 0.02 260 / 0.2)" } as GlassStyle}>
            <CardHeader>
              <CardTitle>No servers registered yet</CardTitle>
              <CardDescription>
                Attach a server via the manager or gateway to see its configuration and status here.
              </CardDescription>
            </CardHeader>
          </Card>
        )}
      </main>
    </div>
  )
}

function App() {
  return <ServerDashboard />
}

export default App
