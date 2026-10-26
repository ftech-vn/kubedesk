import { Component, type ComponentType, type ReactNode } from "react";
import { Puzzle } from "lucide-react";
import { reportError, usePlugins } from "./registry";
import { Button } from "../components/ui";

/**
 * PluginBoundary keeps a plugin's component that crashes from breaking the
 * page around it: it says which plugin failed, and offers to try again.
 */
export class PluginBoundary extends Component<{ plugin: string; children: ReactNode; inline?: boolean }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error) {
    reportError(this.props.plugin, error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    if (this.props.inline) return null;
    return <PluginCrash plugin={this.props.plugin} error={this.state.error} retry={() => this.setState({ error: null })} />;
  }
}

/** pluginIcon draws a plugin's icon, or a puzzle piece, and nothing if the icon crashes. */
export function pluginIcon(plugin: string, Icon: ComponentType<{ size?: number }> | undefined, size: number): ReactNode {
  const I = Icon ?? Puzzle;
  return (
    <PluginBoundary plugin={plugin} inline>
      <I size={size} />
    </PluginBoundary>
  );
}

function PluginCrash({ plugin, error, retry }: { plugin: string; error: Error; retry(): void }) {
  const name = usePlugins((s) => s.names[plugin] ?? plugin);
  return (
    <div className="plugin-crash">
      <div className="plugin-crash-head">
        <Puzzle size={15} />
        <b>{name}</b> crashed
        <div className="spacer" />
        <Button size="sm" variant="ghost" onClick={retry}>
          Try again
        </Button>
      </div>
      <pre className="error-message">{error.stack ?? error.message}</pre>
    </div>
  );
}
