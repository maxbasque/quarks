import { assert, assertEquals } from "@std/assert";
import { join } from "@std/path";
import { deleteSecret, expandTokens, listSecretKeys, loadSecrets, readSecretKeys, setSecrets } from "./secrets.ts";

function secretsFile(content?: string, mode = 0o600): string {
  const path = join(Deno.makeTempDirSync(), "secrets.yaml");
  if (content !== undefined) {
    Deno.writeTextFileSync(path, content, { mode });
    Deno.chmodSync(path, mode);
  }
  return path;
}

Deno.test("setSecrets appends to an empty file", () => {
  const path = secretsFile();
  setSecrets(path, { spotify_client_id: "abc123" });
  assertEquals(readSecretKeys(path, "spotify_client_id").spotify_client_id, "abc123");
});

Deno.test("setSecrets preserves comments and other keys", () => {
  const path = secretsFile(`# Your Reddit home feed — reddit.com/prefs/feeds/
reddit_home: "https://www.reddit.com/.rss?feed=XXXXXXXX&user=YOURNAME"
`);
  setSecrets(path, { reddit_home: "https://www.reddit.com/.rss?feed=NEW&user=me" });
  assert(Deno.readTextFileSync(path).includes("# Your Reddit home feed"), "comment was dropped");
  assertEquals(loadSecrets(path).reddit_home, "https://www.reddit.com/.rss?feed=NEW&user=me");
});

Deno.test("setSecrets adds a new key alongside existing ones", () => {
  const path = secretsFile("reddit_home: https://reddit.example/.rss\n");
  setSecrets(path, { spotify_refresh_token: "rt-1" });
  const all = loadSecrets(path);
  assertEquals(all.reddit_home, "https://reddit.example/.rss");
  assertEquals(all.spotify_refresh_token, "rt-1");
});

Deno.test("setSecrets fixes loose permissions", () => {
  const path = secretsFile("reddit_home: https://reddit.example/.rss\n", 0o644);
  setSecrets(path, { spotify_client_id: "abc" });
  assertEquals(Deno.statSync(path).mode! & 0o077, 0);
});

Deno.test("readSecretKeys: a missing file is empty", () => {
  assertEquals(readSecretKeys(secretsFile(), "spotify_client_id"), {});
});

Deno.test("readSecretKeys omits unset keys", () => {
  const got = readSecretKeys(
    secretsFile("reddit_home: https://reddit.example/.rss\n"),
    "reddit_home",
    "spotify_client_id",
  );
  assert(!("spotify_client_id" in got));
  assert(got.reddit_home);
});

Deno.test("listSecretKeys: a missing file is empty", () => {
  assertEquals(listSecretKeys(secretsFile()), []);
});

Deno.test("listSecretKeys is sorted and skips empty values", () => {
  assertEquals(listSecretKeys(secretsFile('zeta: x\nreddit_home: y\nblank: ""\n')), ["reddit_home", "zeta"]);
});

Deno.test("deleteSecret removes only that key", () => {
  const path = secretsFile("# keep me\nreddit_home: y\nspotify_client_id: z\n");
  deleteSecret(path, "spotify_client_id");
  assert(Deno.readTextFileSync(path).includes("# keep me"), "unrelated comment was dropped");
  const all = loadSecrets(path);
  assert(!("spotify_client_id" in all));
  assertEquals(all.reddit_home, "y");
});

Deno.test("deleteSecret of a missing key is a no-op", () => {
  const path = secretsFile("reddit_home: y\n");
  deleteSecret(path, "does_not_exist");
  assertEquals(loadSecrets(path).reddit_home, "y");
});

Deno.test("deleteSecret on a missing file is a no-op", () => {
  deleteSecret(secretsFile(), "anything");
});

Deno.test("expandTokens refuses a breakout", () => {
  const got = expandTokens(`feeds: [ "\${secret:a}", "\${secret:b}" ]`, {
    a: "https://ok.example/rss?x=1&y=2",
    b: 'x" ]\nwidgets: [',
  });
  assertEquals(got, `feeds: [ "https://ok.example/rss?x=1&y=2", "" ]`);
});
