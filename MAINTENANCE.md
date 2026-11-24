# daed (self-maintained dae-wing + dashboard)

This repository combines the archived upstream `daeuniverse/dae-wing` (`b089b56`)
and `daeuniverse/daed` (`v2.1.1`) dashboard into a single repository maintained
as a Git submodule (`package/custom/daed/src`) in the OpenWrt tree.

## Layout

- Root (`/`) — `dae-wing` Go GraphQL backend (`b089b56` + local router fixes).
- `webrender/web/` — prebuilt static dashboard assets embedded into `/usr/bin/daed`
  so OpenWrt builds do not require Node.js/pnpm.
- `web-src/` — full `daed v2.1.1` pnpm frontend workspace (`apps/web`, `packages/`).

## Rebuilding the dashboard

After editing files under `web-src/`:

```sh
cd web-src
pnpm install --frozen-lockfile
pnpm --filter @daed-revived/web build
rm -rf ../webrender/web
cp -a apps/web/dist ../webrender/web
```

Then commit the updated `web-src/` and `webrender/web/` in this submodule and
bump `PKG_RELEASE` in `package/custom/daed/Makefile`.
