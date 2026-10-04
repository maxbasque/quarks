// Helpers shared by the tests.

import YAML from "yaml";
import { parseWidget, type WidgetConfig } from "./core/provider.ts";

// widgetConfig builds a WidgetConfig the same way config loading does.
export function widgetConfig(src: string): WidgetConfig {
  return parseWidget(YAML.parse(src));
}

export interface TestServer extends AsyncDisposable {
  url: string;
  close(): Promise<void>;
}

// serve starts a local HTTP server on a free port, like httptest.NewServer.
// Use it with `await using`, or call close().
export function serve(handler: (req: Request) => Response | Promise<Response>): TestServer {
  let url = "";
  const server = Deno.serve({ hostname: "127.0.0.1", port: 0, onListen: () => {} }, handler);
  url = `http://127.0.0.1:${(server.addr as Deno.NetAddr).port}`;
  const close = () => server.shutdown();
  return { url, close, [Symbol.asyncDispose]: close };
}

// serveDir serves the files in dir, like http.FileServer.
export function serveDir(dir: URL): TestServer {
  return serve((req) => {
    const path = new URL(req.url).pathname;
    try {
      return new Response(Deno.readFileSync(new URL("." + path, dir)));
    } catch {
      return new Response("not found", { status: 404 });
    }
  });
}

export function json(v: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(v), {
    ...init,
    headers: { "Content-Type": "application/json", ...(init.headers ?? {}) },
  });
}

export const bg = () => new AbortController().signal;
