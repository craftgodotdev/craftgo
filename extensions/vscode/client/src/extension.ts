// CraftGo VSCode extension client.
//
// Spawns the `craftgo-lsp` binary as a language server and wires it to
// VSCode via vscode-languageclient. The binary path is resolved from the
// `craftgo.serverPath` setting first, falling back to whatever the user
// has on $PATH (the canonical install path is `go install
// github.com/dropship-dev/craftgo/cmd/craftgo-lsp@latest`).

import * as path from "node:path";
import { ExtensionContext, window, workspace, commands } from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export async function activate(context: ExtensionContext): Promise<void> {
  const cfg = workspace.getConfiguration("craftgo");
  const explicit = cfg.get<string>("serverPath", "").trim();
  const command = explicit || "craftgo-lsp";

  const serverOptions: ServerOptions = {
    run: { command, transport: TransportKind.stdio },
    debug: { command, transport: TransportKind.stdio },
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", language: "craftgo" },
    ],
    synchronize: {
      fileEvents: workspace.createFileSystemWatcher("**/*.craftgo"),
    },
  };

  client = new LanguageClient(
    "craftgo",
    "craftgo Language Server",
    serverOptions,
    clientOptions,
  );

  context.subscriptions.push(
    commands.registerCommand("craftgo.restartServer", async () => {
      if (!client) return;
      await client.stop();
      await client.start();
      window.showInformationMessage("craftgo language server restarted.");
    }),
  );

  try {
    await client.start();
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    window.showErrorMessage(
      `craftgo: failed to start ${command}. ` +
        `Install with 'go install github.com/dropship-dev/craftgo/cmd/craftgo-lsp@latest' ` +
        `and ensure it is on PATH (or set craftgo.serverPath). Details: ${message}`,
    );
  }

  // Suppress unused import warning when path module is not strictly needed
  // — keep it so future config-relative resolution can use it cleanly.
  void path;
}

export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  return client.stop();
}
