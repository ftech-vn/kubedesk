import { Component, type ReactNode } from "react";
import { ErrorBox, Button } from "./ui";

/** ErrorBoundary keeps a crash in one part of the page from blanking the app. */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null; key?: string }> {
  state: { error: Error | null; key?: string } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  static getDerivedStateFromProps(props: { resetKey?: string }, state: { error: Error | null; key?: string }) {
    if (props.resetKey !== state.key) return { error: null, key: props.resetKey };
    return null;
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <ErrorBox
        title="This view crashed"
        message={this.state.error.stack ?? this.state.error.message}
        action={<Button onClick={() => this.setState({ error: null })}>Try again</Button>}
      />
    );
  }
}
