# File Station

[简体中文](README.md) | **English**

A single-administrator web file manager for deployment as a regular Linux user, with Docker, or on NixOS. It combines a React + TypeScript frontend, a Go HTTP server, and persistent SQLite state. File contents stay directly in shared directories; refresh to see changes made on the host or through Samba.

The application must run as a non-root user. It does not provide multi-user Linux identity mapping, privilege escalation, or system administration capabilities. **The first startup has no shares by default: home directories, mount points, and all other paths are never shared automatically.** After signing in, add directories manually in **Settings → Share settings**. In a container, mount the directory first, then add its container-side path.

## Interface preview

![File Station file manager in Chinese, showing shared-directory navigation, a file list, and the file operations toolbar](docs/screenshots/files-list-zh.png)

The screenshot uses demo data and the Chinese interface. A fresh installation still starts with no shares; add your own directories.

## Main features

- Administrator password sign-in, 12-hour sessions, and CSRF protection. Passwords are hidden by default; use the eye button on the right of the input to show or hide them. A dedicated **Sign out** button appears at the top right, retaining both its icon and text on narrow screens.
- Simplified Chinese and English on the sign-in page and throughout the application, with immediate switching through the language selector. Dates, numbers, and sorting adapt to the selected language. The initial language follows the browser language (Simplified Chinese for Chinese browsers, English otherwise). Manual choices are saved in the current browser and survive refreshes and sign-outs. Language switching still works when local storage is blocked, but the choice is not saved. Language settings do not change file names, share names, or file contents.
- Browse and manage multiple shared directories, with read-only shares, name search within the current directory, sorting, multi-selection, and list/grid views. The grid shows only icons and names; choose a sort field and ascending/descending order from the **Sort** menu next to the select-all checkbox. Folders always appear before files. In list view, sort using column headers. Advanced settings can save hidden-item visibility and which list columns are shown. List headers and rows use matching column widths; scroll horizontally on narrow screens to see the remaining columns.
- General settings lets you choose and preview a date display format, applied consistently to file modification/creation times, recycle-bin deletion times, and preview windows. Saved changes take effect immediately and persist across refreshes and restarts.
- General settings also lets you choose and preview octal permissions (such as `0755`, the default) or symbolic permissions (such as `rwxr-xr-x`) for file-list and recycle-bin permission columns, including special bits shown as `s/S/t/T`. Saved changes take effect immediately and persist across refreshes and restarts; actual permissions and sorting remain unchanged. Enable the permission column under **Advanced settings → Columns**.
- A sidebar starting with **Shared spaces**, with share navigation, the recycle bin, and keyboard shortcuts.
- Create folders, rename, copy, move, upload, download, and calculate sizes. For name conflicts, skip, replace files, or keep both copies.
- A recycle bin with restore and confirmed permanent deletion; background tasks, upload progress, cancellation, and retries after failure.
- PNG/JPEG/GIF/WebP image previews that initially fit the window while preserving aspect ratio (without enlarging small images). Zoom with Ctrl + mouse wheel or buttons, drag to pan when zoomed in, and restore fit-to-window with one click. Preview and edit UTF-8 text up to 1 MiB.
- Native MP4/WebM video playback with seeking, volume control, and browser fullscreen. Original files are read on demand, without autoplay or server-side transcoding. H.264 + AAC is recommended for MP4; actual codec support depends on the browser and operating system. Unsupported or damaged files can still be downloaded for viewing elsewhere.
- Markdown rendering, source editing, side-by-side source and preview, and visual editing. Visual changes reserialize Markdown and may normalize whitespace, lists, tables, or unsupported syntax. Use source editing when exact preservation is required.
- **About File Station** in Advanced settings shows an introduction, the current version, the project URL, and the open-source license. **Check for updates** connects to GitHub from the server to check the latest stable Release and provide its release-page link; it does not download or install updates automatically. Missing Releases, versions that cannot be compared, network errors, and GitHub rate limits are reported separately.

Failed copy, move, and recycle-bin entries in the task center can retry unfinished items, creating a new task while retaining the original record. Copy and move retries can use a different conflict policy. Saved retry parameters remain available after refreshes and restarts; historical tasks from older versions that did not save these parameters must be started again. Failed uploads can be retried on the current page; after refreshing or signing out, select the files again.

Changing the language reinitializes the Markdown visual editor toolbar. The current document and unsaved state are retained, but the visual editor's undo history, cursor position, and uncommitted insertion dialogs are reset. Finish any dialog operation before switching languages.

## Local build and run

Requires Linux, Go 1.26, Node.js 24, and npm; alternatively, use `nix develop path:.`. From the repository root, as a regular user:

```sh
npm --prefix web ci
npm --prefix web run build
go build -o /tmp/file-station ./cmd/file-station

mkdir -p "$HOME/.config/file-station" "$HOME/.local/state/file-station"
chmod 700 "$HOME/.config/file-station" "$HOME/.local/state/file-station"
read -rs -p 'Administrator password: ' password
printf '\n'
(umask 077; printf '%s' "$password" > "$HOME/.config/file-station/password")
unset password
```

Create `$HOME/.config/file-station/config.json`, replacing `/absolute/home` with the actual absolute path. JSON does not expand environment variables:

```json
{
  "ip": "127.0.0.1",
  "port": 8080,
  "stateDir": "/absolute/home/.local/state/file-station",
  "passwordFile": "/absolute/home/.config/file-station/password",
  "umask": "0022",
  "secureCookies": false,
  "shares": []
}
```

```sh
chmod 600 "$HOME/.config/file-station/config.json" "$HOME/.config/file-station/password"
/tmp/file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

Open <http://127.0.0.1:8080> and add existing directories in **Settings → Share settings**. Alternatively, set `password` directly in JSON and remove `passwordFile`; exactly one of the two sources must be nonempty.

### Testing in the current terminal

`test/update-service.sh` keeps its original path, but now only builds and runs in the current terminal; it no longer updates a service. From the repository root, run:

```sh
./test/update-service.sh -config /absolute/path/to/test-config.json
```

The script builds the repository's Nix package with `nix build "path:$repo_root#default" --no-link --print-out-paths`, then uses `exec` to run `bin/file-station` from the build output, forwarding all arguments unchanged. The wrapper already supplies the static asset path, so `-static` is unnecessary. Logs appear directly in the current terminal; press Ctrl+C to exit. The script does not query, start, stop, or restart any system or user service, change the `$HOME/.local/state/file-station/current` link, or automatically select or create configuration.

**To run alongside an existing service, prepare a separate test JSON file yourself, using a different port and an independent absolute `stateDir`. Never reuse the service's state directory.** For example, replace `/absolute/home` with the actual absolute path and save the following as `/absolute/path/to/test-config.json` (JSON does not expand environment variables):

```json
{
  "ip": "127.0.0.1",
  "port": 8081,
  "stateDir": "/absolute/home/.local/state/file-station-test",
  "passwordFile": "/absolute/home/.config/file-station-test/password",
  "umask": "0022",
  "secureCookies": false,
  "shares": []
}
```

Prepare the test password file and directories using the safe procedure under **Local build and run**: read the password interactively, write it with `umask 077`, set private directories to `0700` and the JSON and password files to `0600`, and keep them outside all shares. Check that port `8081` is available yourself, then open <http://127.0.0.1:8081> after starting. The script does not automatically isolate configuration or state; the application accesses data and writes test state according to the supplied configuration. Use dedicated test shares for file operations.

### Updating an existing user service

If `file-station.service` has an `ExecStart` pointing to `$HOME/.local/state/file-station/current/bin/file-station`, manually build and update that link from the repository root, restarting the corresponding user service only if the build succeeds:

```sh
nix build path:.#default --out-link "$HOME/.local/state/file-station/current" &&
  systemctl --user restart file-station.service
```

These commands deliberately update and restart the existing service; the foreground test script does neither. Migrate old-name services first as described below. NixOS system services are still updated through `nixos-rebuild`.


## Configuration files and environment variables: two independent sources

Configuration files are recommended for local and NixOS deployments. Docker defaults to supplying the password directly through `PASSWORD`. The application neither detects the deployment environment automatically nor combines the two sources of application configuration:

- **File mode**: Select a JSON file only through a nonempty `-config /absolute/config.json`. Configuration then comes exclusively from **built-in defaults + the JSON file**. Go configuration loading completely ignores `ConfigPath`, `PASSWORD`, and `UMASK`, including empty or invalid values. They neither override the file nor fill in missing fields. Startup fails if the file cannot be read, the JSON is invalid, or it contains unknown fields or extra JSON values.
- **Environment-variable mode**: `-config` is empty by default. When it is omitted or passed as `-config ''`, application configuration comes from `PASSWORD`, `UMASK`, and the case-sensitive `ConfigPath`. `ConfigPath` is the persistent state directory for saved settings, defaulting to `/var/lib/file-station`. It must be a nonempty absolute path and must not be the root directory `/` (including paths that resolve to root after normalization or successful symlink resolution). Explicitly empty values and relative paths are invalid. It is never a JSON file path. Ordinary non-root absolute paths may contain spaces. The listener is fixed at `0.0.0.0:8080`, the initial share list is empty, and `secureCookies` is `false`. Use JSON to customize the listener or enable secure cookies.

`-config` does not read any environment variable to select the configuration file. The static asset directory is **specified only by `-static`**, defaults to `web/dist`, and must contain the frontend build output. The Nix wrapper and Docker image command already specify an absolute path inside the package. The static directory is not part of the JSON configuration. Container entrypoint rules for preparing the state directory are independent of Go configuration loading; file mode must also follow the mount and permission requirements below.

| JSON field (file mode only) | Built-in default and description |
| --- | --- |
| `ip` | `127.0.0.1`; an IPv4/IPv6 address without a port or IPv6 brackets; `0.0.0.0` listens on all IPv4 interfaces |
| `port` | `8080`; an integer from `1–65535` |
| `stateDir` | `/var/lib/file-station`; a separate, private, persistent directory with a nonempty absolute path. Regular users usually need to override it. This field determines the directory in file mode; `ConfigPath` is not read |
| `password` | No default password. Exactly one of this field and `passwordFile` must provide a nonempty source; at least 6 bytes, with at least 12 bytes recommended |
| `passwordFile` | Absolute path to a runtime password file; trailing newlines are stripped when reading, while other whitespace is retained |
| `passwordSourceFile` | Empty by default; advanced deployment metadata that additionally prevents shares from covering the original password source path. This file is not read and is not an authentication password source |
| `umask` | `"0022"`; must be a 3- or 4-digit octal string with a value from `0000` to `0777`, not a JSON number or an empty string |
| `secureCookies` | `false`; set to `true` for HTTPS deployments |
| `shares` | `[]`; optional initial import. Subsequent share settings are stored in the state database |

Docker deployments use only the following five environment variables, without aliases for old names or other short names:

| Environment variable | Default and description |
| --- | --- |
| `PUID` | Defaults to `1000` for the root entrypoint. UID must be a decimal integer from `1` to `4294967294`, without leading zeros; an explicitly empty value is invalid. Not application configuration |
| `PGID` | Defaults to `1000` for the root entrypoint; the same format rules apply as for UID. Not application configuration |
| `UMASK` | `"0022"`; the same format as JSON `umask`. An explicitly empty value is invalid; completely ignored in file mode |
| `PASSWORD` | No default password; required in environment-variable mode, at least 6 bytes, with at least 12 bytes recommended. Completely ignored in file mode |
| `ConfigPath` | `/var/lib/file-station`; persistent directory for SQLite state such as settings, shares, tasks, and sessions. It must be a nonempty absolute path that does not point to the root directory `/` (including after normalization or successful symlink resolution). Explicitly empty values and relative paths are invalid. It is not a JSON file path. Go file mode ignores it, but the container entrypoint always prepares the state directory according to it |

`passwordSourceFile` supports deployments such as NixOS `LoadCredential` that copy the original password to a private credential path. `passwordFile` points to the credential actually read, while `passwordSourceFile` only protects the original absolute path and any resolvable real path against shares covering their ancestor directories. The service does not need permission to read or traverse the original password file. Unresolvable portions receive best-effort protection based on the known absolute path and resolvable ancestors, without preventing startup. This privacy metadata takes effect on every startup, rather than being a one-time import like `shares`. Changing the authentication password source does not clear it automatically. To remove it intentionally, delete or clear `passwordSourceFile` in JSON.

Password files and configuration files containing passwords should have mode `0600`, remain outside all shares, and never be committed to the repository. Docker can use `PASSWORD` directly, but plaintext environment variables and Compose configuration can be read through `docker inspect` or by authorized host administrators. Do not commit passwords to version control or write them into image layers or the Nix store. Read-only JSON file mode is an option when you want less exposure through environment variables. Local deployments can continue using `passwordFile`; NixOS must use a runtime password file. Changes to the listener, password, umask, or other startup configuration require a restart. Saving shares through the UI does not.

The **Settings** gear button at the top opens the settings category page. Under **General settings**, choose the default format, `yyyy-MM-dd`, `yyyy/MM/dd`, `dd/MM/yyyy`, or `MM/dd/yyyy`. The default date format follows the interface language; fixed formats do not. All formats use the browser's local time zone and a 24-hour clock, including hours, minutes, and seconds. Click **Save general settings** to apply your selection. Use **Share settings** to manage shared directories and access permissions. **Back to settings** returns to the category page while retaining unsaved edits within the current dialog. Use **Close** at the top right or Esc to close settings; reopening returns to the category page. Settings cannot be closed or returned to the category page while saving settings or refreshing share status. The language selector remains separate, and settings pages follow the selected language. **Advanced settings** still opens separately from the file toolbar.

### Directory permissions and persistent state

There is no manually configured `allowedRoots` allowlist or fixed `/etc` or `/nix` path denylist. The directory picker browses from `/` and lists directories the process can read and traverse. Whether a share can be added, read, or written depends on the **service process's actual UID, primary/supplementary groups, Unix permissions, ACLs, and mount options**. Every parent directory requires traversal permission; listing a directory requires read permission, and writes also require the relevant write permissions. File operations do not run as the user signed into the browser, and the application does not automatically `chown` shared data.

Shares must be existing, accessible, non-overlapping directories. Share root paths are normalized and host symlinks resolved; file operations inside a share still prohibit following symlinks. `readOnly` is an additional application-level restriction and cannot grant permissions the operating system does not provide. Use read-only mounts or ACLs for operating-system-level read-only protection. Being able to browse `/` in the directory picker does not mean it can be shared: broad ancestor directories covering private state or password files cannot be added as shares.

The persistent state directory is specified by `ConfigPath` in environment-variable mode or JSON `stateDir` in file mode. It has mode `0700`, must be owned by the runtime UID, must not overlap any share, and must not be placed in a temporary directory or deleted as part of deployment. Its `state.sqlite` stores settings, shares, tasks, sessions, and recycle-bin metadata; it is not the startup JSON file. Sharing an entire home directory may be rejected because it contains state or passwords; choose specific data subdirectories instead. Administrators must also keep configuration files containing inline passwords outside shares.

### Initial import and share management

`shares` is imported only once, when the state database is first initialized. Initialization is recorded even if the list is initially empty. Later JSON changes, restarts, and NixOS generation rollbacks do not overwrite saved shares. Environment-variable mode offers no initial import setting. The optional JSON import format is shown below; it is normally unnecessary:

```json
[{"id":"documents","name":"文档","path":"/srv/documents","readOnly":false}]
```

Each entry requires `name` and `path`. Omit `id` to generate it automatically; `readOnly` defaults to `false`. Preserve the existing state database and share IDs to avoid breaking task and recycle-bin associations.

After initialization, add, edit, and remove shares in **Settings → Share settings**; saved changes take effect immediately. Removing a share deletes only its configuration, not directory contents. While any file request (including uploads and downloads) is being processed, share changes temporarily return HTTP 409. A share also cannot be modified or removed while it has active background tasks. A nonempty recycle bin prevents changing the path or removing the share; restore or permanently delete its contents first. When a directory disappears, a mount is not ready, or permissions are insufficient, saved shares are retained and marked unavailable, never automatically deleted or migrated.

## Docker / Compose

The repository's `compose.yaml` still builds from local source by default. To deploy a published GHCR image, remove the service's `build: .` and replace its `image` with a pinned version, keeping all other environment variables, ports, mounts, and security settings:

```yaml
    image: ghcr.io/cyl96/file-station:1.0.0
```

`1.0.0` is only an example, not a claim that this version is published; choose a successfully published version. Images support `linux/amd64` and `linux/arm64`; Docker selects the architecture automatically. You can also run `docker pull ghcr.io/cyl96/file-station:1.0.0` directly. Anonymous pulls require the GHCR package permissions described in the release section below.

The image uses a multi-stage Node.js 24 and Go 1.26 build. The final image contains Alpine, CA certificates, `su-exec`, a static Go binary, and the frontend; neither Node.js nor Go is needed at runtime. The entrypoint briefly runs as root to validate `PUID` / `PGID` (defaulting to `1000:1000` when unset), prepare the private state directory specified by `ConfigPath` (default `/var/lib/file-station`), and adjust ownership of that directory alone. It then drops privileges through `su-exec`. The Go service, now running as that non-root identity, sets its actual state directory's permissions to `0700`. **The application process always runs as non-root.** It does not recursively change existing state contents, let alone shared data. Choose an identity directly with `docker run -e PUID=… -e PGID=…`; Compose `user:` is unnecessary. The entrypoint requires a nonempty absolute `ConfigPath` whose directory itself is not a symlink, and also rejects paths resolving to the root directory `/`.

The repository's `compose.yaml` publishes only to host `127.0.0.1:8080` by default and mounts `./state` at `${ConfigPath-/var/lib/file-station}` for persistence. The mount target stays in sync with the `ConfigPath` passed to the container. Compose secrets are no longer used. Its only environment variables are the following five. `PASSWORD` has no default password and may be empty in file mode; environment-variable mode checks for at least 6 bytes at startup:

```yaml
environment:
  PUID: '${PUID-1000}'
  PGID: '${PGID-1000}'
  UMASK: '${UMASK-0022}'
  PASSWORD: '${PASSWORD-}'
  ConfigPath: '${ConfigPath-/var/lib/file-station}'
```

For a **fresh deployment** using the GHCR image configured above, prepare the following in Bash at the repository root:

```sh
# Use the current regular user, or specify another non-root UID/GID.
export PUID="$(id -u)" PGID="$(id -g)" UMASK='0022' ConfigPath='/var/lib/file-station'
read -rs -p 'Administrator password (at least 6 bytes; 12+ recommended): ' PASSWORD
printf '\n'
export PASSWORD
mkdir -p state
docker compose pull && docker compose up -d
unset PASSWORD
```

If you prefer to enter the password directly, create a private `.env` file (mode `0600`, never committed), then run `docker compose pull && docker compose up -d`:

```dotenv
PUID=1000
PGID=1000
UMASK='0022'
PASSWORD='replace-with-your-own-password-at-least-12-bytes'
ConfigPath='/var/lib/file-station'
```

`PASSWORD` has no default; you must replace the example value. **If a password entered directly in Compose YAML contains `$`, write it as `$$`. In `.env`, enclose the password in single quotes to avoid variable interpolation.** Do not rely on YAML single quotes alone to prevent Compose interpolation, and never use a documentation example password as your real password.

If you retain the repository's default local-build configuration instead, use `docker compose up -d --build`. Preserve `./state` during updates and container recreation; do not delete the state directory or database.

The recommended custom state path example is `ConfigPath=/config`: set `ConfigPath='/config'` in your private `.env`. Compose then mounts `./state` at `/config` (that is, `./state:/config`), and persistent state such as SQLite is stored directly in that directory. Do not enter `/config/config.json` or set `ConfigPath=''`.

The entrypoint prepares only the state directory itself. **When changing UID/GID for existing data, administrators must first stop the service, make a backup, and safely migrate ownership of the private state directory and all existing contents while preserving permissions and contents.** Do not rely on recursive repair by the entrypoint. State must remain persistent: deleting or replacing the state database loses share settings, tasks, sessions, and recycle-bin associations. For rootless Docker or user namespace remapping, prepare permissions according to the host UID mapping.

Compose does not set `user:` by default. It retains `cap_drop: [ALL]`, adds only `CHOWN`, `SETUID`, and `SETGID` for entrypoint directory preparation and privilege dropping, and keeps `no-new-privileges:true`. If you explicitly use a non-root `--user UID:GID`, unset `PUID` / `PGID` inherit the current identity instead of the root entrypoint's defaults. The entrypoint neither elevates privileges nor changes directory ownership; the state directory must already belong to the current identity, and the Go service still sets its permissions to `0700`. Explicit `PUID` / `PGID` values that differ from the current identity cause an error.

Compose has **no shared-data mounts by default**. For example, uncomment and adjust the following as needed:

```yaml
volumes:
  - './state:${ConfigPath-/var/lib/file-station}'
  - /srv/documents:/mnt/documents
  # - /srv/archive:/mnt/archive:ro
```

After recreating the container, add **`/mnt/documents`** manually in the UI, not the host path `/srv/documents`. A mount does not automatically become a share, and the container cannot browse host paths that have not been mounted into it. Host directory UID/GID ownership, ACLs, and read-only mounts still apply. Switching identities with `su-exec` clears supplementary groups; do not rely on Docker `group_add` / `--group-add` to retain access. Set primary-group permissions or ACLs for the actual process identity. Do not switch to root for convenience or recursively change ownership of existing shared data.

You can also build locally and run without Compose (prepare `state` first; these commands read the password from the terminal, not Compose's `.env`):

```sh
mkdir -p state
read -rs -p 'Administrator password (at least 6 bytes; 12+ recommended): ' PASSWORD
printf '\n'
export PASSWORD
docker build -t file-station:local .
docker run -d --name file-station \
  --cap-drop ALL \
  --cap-add CHOWN --cap-add SETUID --cap-add SETGID \
  --security-opt no-new-privileges:true \
  -p 127.0.0.1:8080:8080 \
  -e PUID="${PUID-1000}" -e PGID="${PGID-1000}" \
  -e UMASK='0022' -e PASSWORD -e ConfigPath=/var/lib/file-station \
  --mount "type=bind,src=$PWD/state,dst=/var/lib/file-station" \
  file-station:local
unset PASSWORD
```

The image command is `-static /usr/share/file-station`. Environment-variable mode always listens on container `0.0.0.0:8080`, uses `ConfigPath` for state (default `/var/lib/file-station`), and starts with no shares; the host mapping remains limited to `127.0.0.1:8080`. If you change a direct `docker run` invocation to `-e ConfigPath=/config`, also change the state mount above to `-v "$PWD/state:/config"`. To expose the service beyond localhost, explicitly adjust the port mapping and firewall. Compose passes through only the five variables listed above, not other variables from the host shell or `.env` automatically.

### Optional JSON file mode for Docker

To customize the listener, enable secure cookies, or use a password file, mount a read-only JSON file and explicitly pass `-config`. A custom persistent state directory alone does not require switching modes; use `ConfigPath`. For example, create a private `config.json` (mode `0600`, outside shares) and enter your actual password directly:

```json
{
  "ip": "0.0.0.0",
  "port": 8080,
  "stateDir": "/var/lib/file-station",
  "password": "replace-with-your-own-long-password",
  "umask": "0022",
  "secureCookies": false,
  "shares": []
}
```

Make sure the runtime UID can read the file, then enable the optional configuration mount in Compose and set the full command (keep `-static` when overriding the image CMD):

```yaml
environment:
  PUID: '${PUID-1000}'
  PGID: '${PGID-1000}'
  UMASK: '${UMASK-0022}'
  PASSWORD: '${PASSWORD-}'
  ConfigPath: '${ConfigPath-/var/lib/file-station}'
volumes:
  - './state:${ConfigPath-/var/lib/file-station}'
  - ./config.json:/etc/file-station/config.json:ro
command: ["-config", "/etc/file-station/config.json", "-static", "/usr/share/file-station"]
```

This JSON uses the default state directory, so keep `ConfigPath=/var/lib/file-station` (or leave it unset). If you switch to the recommended `ConfigPath=/config`, also change JSON `stateDir` to `/config`; Compose will mount `./state:/config` accordingly.

For direct `docker run`, add `--mount "type=bind,src=$PWD/config.json,dst=/etc/file-station/config.json,readonly"` before the image name in the complete command above, and change the final image command to:

```sh
  file-station:local -config /etc/file-station/config.json -static /usr/share/file-station
```

You may instead use `passwordFile` in JSON in place of `password` and mount the corresponding password file read-only. It must be readable by the runtime UID and remain outside shares.

In file mode, **Go configuration loading completely ignores `ConfigPath`, `PASSWORD`, and `UMASK`, regardless of whether they are empty or valid**; they cannot serve as JSON fallbacks. `PUID` / `PGID` still determine the container identity. However, the container entrypoint still validates `ConfigPath` and prepares the runtime state directory it specifies. It does not read JSON or change the preparation path based on `-config`. Normally, keep JSON `stateDir`, `ConfigPath`, and the persistent mount target aligned. If you intentionally use different paths, you must provide a persistent mount for JSON `stateDir` and prepare its directory and permissions yourself; the entrypoint will not prepare it. JSON must use container-side paths. In container file mode, explicitly set `ip: "0.0.0.0"`; otherwise, JSON's default `127.0.0.1` makes the service unreachable through port mappings. For HTTPS, set `secureCookies: true` in JSON. The static asset directory remains independently specified by an explicit `-static` argument.

## GitHub Releases and binary installation

Maintainers must set the same stable version in `internal/server/version.txt`, `web/package.json`, and `web/package-lock.json` (both the top-level and root-package versions), commit it, and merge it into `main`. Push a matching `vMAJOR.MINOR.PATCH` tag for that commit. For example, with version `1.0.0`:

```sh
git switch main
git pull --ff-only
git tag -a v1.0.0 -m "File Station v1.0.0"
git push origin v1.0.0
```

`.github/workflows/release.yml` checks the stable tag format, matching versions, and that the commit is on the default branch. It builds and tests on native Linux amd64 and arm64 runners with Go 1.26 and Node.js 24. Prerelease tags such as `v1.0.0-rc.1` are not supported and will not publish.

After both builds succeed, it generates release notes and publishes a GitHub Release containing `file-station-v1.0.0-linux-amd64.tar.gz`, `file-station-v1.0.0-linux-arm64.tar.gz`, and `SHA256SUMS`. Each archive has a matching top-level directory containing a static Go executable, `web/dist`, the license, and both READMEs; Nix is not required. Only after publication succeeds does it call the Cachix workflow to build and upload Nix packages from the same tagged commit. Ordinary pushes to `main` no longer automatically build the cache.

Stable tags also automatically publish the multi-architecture images `ghcr.io/cyl96/file-station:<version>` (the tag without its leading `v`) and `ghcr.io/cyl96/file-station:latest`, supporting `linux/amd64` and `linux/arm64`. These final image tags are published only after version validation, all builds and tests, native non-root container HTTP smoke tests on both architectures, and GitHub Release publication succeed. Configuring the workflow does not mean an image has already been published.

Updates to `latest` are serialized across releases and compare the embedded image version, allowing only equal or newer versions. A delayed or rerun older release still publishes its pinned version tag without rolling back `latest`.

Release and image publication use the automatic `GITHUB_TOKEN`, with `packages: write` granted to image jobs; no additional PAT is needed. After the first GHCR package is created, a maintainer must set its visibility to **Public** in package settings to permit anonymous pulls. For an existing package, grant this repository **Write** under **Manage Actions access**. Cachix still requires the repository variable `CACHIX_CACHE=cyl96-file-station` and secret `CACHIX_AUTH_TOKEN` with a per-cache write token. A build failure prevents release creation. An upload/publication failure may leave a draft; inspect and handle it before rerunning. Published releases are not overwritten and tags must not be moved; publish a new version for fixes. If only subsequent image publication or Cachix fails, use **Re-run failed jobs** in Actions without publishing the Release again. To build the default branch cache independently, manually run **Nix binary cache** on the default branch.

Download the archive matching your architecture and `SHA256SUMS` from [Releases](https://github.com/CYL96/File-Station/releases). Verify and extract them in the download directory (amd64 `v1.0.0` example; `--ignore-missing` permits downloading only one architecture):

```sh
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf file-station-v1.0.0-linux-amd64.tar.gz
cd file-station-v1.0.0-linux-amd64
./file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

Prepare configuration, a private password, and a writable state directory as described above. Always run as a non-root user. When starting from another working directory, use an absolute path for `-static`. GitHub's automatically generated Source code archives are not binary installers. Nix clients should keep the official `https://cache.nixos.org` substituter alongside Cachix for shared dependencies.

## Nix package and NixOS service

```sh
nix build path:.#file-station
./result/bin/file-station -config "$HOME/.config/file-station/config.json"
```

`default` and `file-station` refer to the same package. The flake supports `x86_64-linux` and `aarch64-linux`. The package wrapper specifies the frontend automatically, so `-static` is unnecessary. The source input and homepage continue to use the actual repository address, `github:CYL96/NixOS-File-Station`.

The NixOS module generates JSON and starts the service with `-config`, so it always uses file mode. The module configuration determines the listener, state, password, privacy metadata, and initial share import; `ConfigPath` / `PASSWORD` / `UMASK` cannot override them. The module always generates `umask: "0077"` and retains systemd `UMask=0077`; it provides no additional umask option.

Add the input to your system flake and import the module. For an existing flake, merge the relevant parts:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    file-station.url = "github:CYL96/NixOS-File-Station";
    file-station.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = inputs@{ nixpkgs, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        inputs.file-station.nixosModules.default
        {
          users.groups.documents = {};
          # Pre-create only a new dedicated data directory; do not recursively change existing files.
          systemd.tmpfiles.rules = [ "d /srv/documents 2770 root documents - -" ];
          services.file-station = {
            enable = true;
            ip = "127.0.0.1";
            port = 8080;
            passwordFile = "/run/secrets/file-station-password";
            stateDir = "/var/lib/file-station";
            user = "file-station";
            group = "file-station";
            supplementaryGroups = [ "documents" ];
            # shares defaults to {}; add /srv/documents manually after the first sign-in.
          };
        }
      ];
    };
  };
}
```

By default, the module creates a dedicated `file-station` system user and group automatically; it no longer depends on a login user named `nixos`. You can set `user` to an existing non-root user declared in `users.users`. The module does not change a custom account's primary group or login settings. `group` is customizable; the module creates that group, and existing group definitions can merge normally. `SupplementaryGroups` adds permissions only to the service process, without changing the account's global group memberships. For example:

```nix
services.file-station = {
  user = "alice";       # users.users.alice is already declared in the system configuration
  group = "file-data";  # The module creates this group; existing definitions can also merge
  supplementaryGroups = [ "documents" ];
};
```

Reusing an existing user shares its UID with its other processes and loses the isolation boundary of a dedicated user. When granting access to existing data, prefer targeted group permissions or ACLs; all parent directories must also be traversable. The module creates no default shares or home directory and does not recursively change shared directories. It creates the state directory with mode `0700`, adjusts ownership/group of private state contents to the selected user/group, and preserves existing state-file permissions.

Before deployment, provide `passwordFile` using a secrets manager or administrator action. It can remain root:root with mode `0600`, and its parent directories may also be accessible only to root. systemd `LoadCredential` passes it to the service's private credential path. In the generated JSON, `passwordFile` points to that credential and `passwordSourceFile` records the original path, preventing UI shares from covering directories containing the password source. The original path is only share-protection metadata: the application does not read it or use it for authentication. Password contents are not written into generated JSON or the Nix store. **Use an absolute runtime path string, never a Nix path literal, `writeText`, or another mechanism that stores the password in a store file.**

The module has removed directory-allowlist-based `ReadWritePaths`/`ReadOnlyPaths` and the `ProtectSystem`, `ProtectHome`, `PrivateTmp`, and `PrivateDevices` filesystem remapping. It no longer imposes a separate set of shared-path permissions. `RemoveIPC=false` prevents service shutdown from removing IPC objects used by other processes with the same user or group; this remains set even when using the default user with a custom shared group. Hardening such as blocking privilege escalation, an empty capability set, and kernel/control-group protections remains in place, and sensitive kernel interfaces remain protected. Access to ordinary data paths is determined by the service user's actual permissions. Shares added through the UI do not automatically create mount dependencies. Configure mounts and service startup order for network disks and similar storage at the system level, for example with `systemd.services.file-station.unitConfig.RequiresMountsFor = [ "/mnt/documents" ];`.

An optional Nix initial import can be written as `shares.documents = { name = "文档"; path = "/srv/documents"; readOnly = false; };`. The attribute name is the stable share ID, and the same first-initialization-only import rule applies.

```sh
sudo nixos-rebuild switch --flake /path/to/system-flake#myhost
systemctl status file-station
journalctl -u file-station
sudo systemctl restart file-station
```

## Migrating from the old name (breaking changes)

The old `nixos-file-station` binary, flake package attribute, `services.nixos-file-station` options, and `nixos-file-station.service` have been removed. Use `file-station` / `services.file-station` / `file-station.service` instead; no compatibility aliases remain. `allowedRoots` has also been removed. Remove it from JSON/Nix configuration and grant access through service-account permissions, ACLs, and mount options instead.

All old `FILE_STATION_*` environment variables have been removed without compatibility aliases. Docker now uses `PUID`, `PGID`, `UMASK`, `PASSWORD`, and `ConfigPath`, with the password supplied directly by default. Remove old Compose secret mounts or migrate them to JSON file mode selected explicitly with `-config`. `ConfigPath` denotes only the persistent state directory, no longer selects startup JSON, defaults to `/var/lib/file-station`, and cannot be empty. Specify the static directory only with `-static`, and move custom listener settings, secure cookies, and similar options into JSON.

**Default permissions for ordinary newly created content are less restrictive**: local/JSON deployments default to `umask: "0022"`, producing `0644` files and `0755` directories instead of the previous `0640` / `0750`. To keep the previous local permissions, explicitly set `"umask": "0027"` in JSON; Docker environment-variable mode can use `UMASK='0027'`. Existing content permissions are not changed automatically. NixOS still uses a fixed `"0077"`.

The default state path has changed from `/var/lib/nixos-file-station` to `/var/lib/file-station`. **During migration, stop the old service and make a backup, then explicitly set `services.file-station.stateDir = "/var/lib/nixos-file-station";` (set JSON `stateDir` in local file mode; set `ConfigPath` in Docker environment-variable mode; in Docker file mode, align `ConfigPath` with JSON `stateDir` and point the persistent mount target at that directory). Preserve the original SQLite database and all state files.** Alternatively, after stopping the service, migrate the entire state to the new path and correctly set ownership for the runtime UID/GID. Do not reset by deleting the state database. The Nix module adjusts the selected state path's ownership to the new service user/group. The Docker entrypoint adjusts only the state directory itself; administrators must safely migrate existing contents. Do not run old and new services against the same state database at the same time. Old bootstrap `shares` do not overwrite the database; existing shares remain and can be corrected in the UI if permissions change.

The session cookie has changed from `nixos_file_station_session` to `file_station_session`. Old browser sessions are incompatible, so sign in again. Migrate custom overrides for the old unit, monitoring, and log commands as well. The repository remote URL has not changed.

### Manually migrating an old user service

The following applies only to the old `nixos-file-station.service` managed by `systemctl --user`, not to NixOS system-service migration. Use the original regular user throughout. First inspect the old unit's `ExecStart`, environment, and drop-ins, along with its actual configuration file, to identify the original `stateDir`, password source, and required startup options. Do not adopt a new default state directory merely because of the rename, and do not regenerate the password.

1. **Build the new package before stopping the old service.** Run from the repository root to create the executable used by the unit below. If the build fails, leave the old service running and do not continue migration:

   ```sh
   systemctl --user cat nixos-file-station.service
   mkdir -p "$HOME/.local/state/file-station"
   nix build path:.#default --out-link "$HOME/.local/state/file-station/current"
   ```

2. Replace the three variables below with **actual absolute paths**, stop the old service, and back up the entire state, configuration, and old unit. The backup directory must be outside the state directory and all shares. If a share covers the entire home directory, do not put the backup in your home directory. Run these commands in order in the same shell:

   ```sh
   config_path='/absolute/path/to/existing-config.json'
   state_dir='/absolute/path/to/existing-state'
   backup_dir='/absolute/private/path/outside-shares/migration-backup'
   umask 077
   systemctl --user stop nixos-file-station.service
   mkdir -p "$backup_dir"
   systemctl --user cat nixos-file-station.service > "$backup_dir/nixos-file-station.service.txt"
   cp -a -- "$config_path" "$backup_dir/config.json"
   cp -a -- "$state_dir" "$backup_dir/state"
   ```

   If the password comes from a separate file, also make a private backup of the actual password source file at `"$backup_dir/password"`, retaining its original file and permissions. If the unit provides the password through credentials or the environment, back up the corresponding source files and settings as well. The unit snapshot includes drop-in contents and must be kept private; also back up source files for custom drop-ins. Do not run other instances using this state directory while the service is stopped. Do not start the new service if the backup fails.

3. Edit `"$config_path"`: remove unsupported `allowedRoots`, **explicitly preserve the original absolute `stateDir`**, and retain the existing password source and necessary settings such as the listener. Exactly one of JSON `password` / `passwordFile` must be nonempty. Selecting JSON ignores `PASSWORD`, and old `FILE_STATION_*` variables no longer apply either. If the old password exists only in the environment, securely migrate it to JSON `password` or a private file referenced by `passwordFile`, without changing the password contents. If using systemd credentials, retain the corresponding `LoadCredential` in the new unit and point JSON `passwordFile` to the actual credential path. Preserve any required `passwordSourceFile` privacy metadata. Do not delete SQLite or other state files or reset shares; existing shares are read from the original database.

4. Create the new user unit. The template below sets `-config` to the actual `config_path` above rather than assuming configuration has moved to a new directory. Restore any necessary credentials, mount dependencies, and custom settings from the old unit. Do not keep the old binary name or startup path:

   ```sh
   mkdir -p "$HOME/.config/systemd/user"
   cat > "$HOME/.config/systemd/user/file-station.service" <<EOF
   [Unit]
   Description=File Station

   [Service]
   ExecStart=%h/.local/state/file-station/current/bin/file-station -config "$config_path"
   Restart=on-failure

   [Install]
   WantedBy=default.target
   EOF
   ```

   If `file-station.service` or its drop-ins already exist, back them up, merge, and review first; do not overwrite them directly. The Nix package already specifies the static asset path, so the old `-static` path is unnecessary.

5. Disable the old unit before reloading and enabling the new one. **Never allow the old and new services to access the same state directory concurrently**:

   ```sh
   systemctl --user disable nixos-file-station.service
   systemctl --user daemon-reload
   systemctl --user enable --now file-station.service
   systemctl --user status file-station.service --no-pager
   ```

   Sign in and confirm the original shares and settings remain. For troubleshooting, use `journalctl --user -u file-station.service`; do not reinitialize by deleting state. For subsequent updates, follow [Updating an existing user service](#updating-an-existing-user-service): manually build to the existing `current` link and restart the corresponding user service. `test/update-service.sh` is only for foreground testing.

## HTTPS, reverse proxies, and firewalls

For LAN deployments, an HTTPS reverse proxy is recommended. Set `secureCookies: true` in JSON, or `services.file-station.secureCookies = true` on NixOS. Docker must switch to JSON file mode for this setting; environment-variable mode does not offer it. Do not enable Secure over plain HTTP, or the browser will not send the session cookie.

- Preserve the original Host at the proxy. The frontend and API must share an origin; do not split them across different domains or ports.
- Background events use SSE. Disable proxy buffering for `/api/events` and configure a sufficiently long connection timeout.
- Set upload size limits as needed.
- The NixOS module does not open the firewall automatically. When using a proxy, open the HTTPS port. When listening directly on the LAN, open the service's TCP port as needed and restrict access to trusted clients.

## File operation limits and backup safety

- Application permission checks do not replace operating-system isolation. Grant the service account only necessary permissions; mount points and existing hard links may expose other data. The share root cannot be deleted, and files outside shares or in unavailable shares cannot be operated on. Internal `.webui-*` paths cannot be accessed through ordinary file APIs.
- Symlinks within shares can be displayed but cannot be entered, downloaded, previewed, or copied. Moving, renaming, and recycle-bin operations act on the link itself. Only regular files and directories are handled, not special files.
- Copying does not preserve Unix ownership, ACLs, extended attributes, hard-link relationships, or original timestamps. Same-name directories cannot be recursively overwritten. Ordinary new files use `0666 & ~umask` and new directories use `0777 & ~umask`, still subject to operating-system permission rules such as ACLs. The default `"0022"` yields `0644` / `0755`; `"0077"` yields `0600` / `0700`. This setting applies only to ordinary creation, uploads, and files/directories produced by copying. Internal state and staging remain private. Text saves, same-filesystem moves, and recycle-bin restores do not change the original content's permissions. NixOS always applies `umask: "0077"` and retains `UMask=0077`.
- Uploads and copies use hidden temporary files in the destination directory and do not silently overwrite. Cancellation does not roll back completed items. Abnormal interruption may leave temporary files behind; administrators should clean them up only after confirming that no tasks remain.
- Text editing is limited to existing regular UTF-8 files of up to 1 MiB without NUL bytes. Files cannot be saved in read-only shares or the recycle bin, or if they are symlinks, lack write permission, or have multiple hard links. Saving must preserve permissions, ownership/group, ACLs, and extended attributes; it is rejected if they cannot all be preserved. Atomic replacement changes the inode and original timestamps.
- Text saves check the version that was read, retaining the draft and reporting a conflict if external changes are detected, without a force-overwrite option. External writes can still occur between the check and replacement: **this is not filesystem-level CAS**. Files with uniformly CRLF line endings retain CRLF; other line endings are normalized to LF after editing.
- Same-filesystem moves are atomic renames. Cross-filesystem moves copy first, then delete, and **are not an overall transaction**. Both source and destination may exist after a failure.
- Background task success/failure states are persisted. Unfinished tasks are marked interrupted on restart and are not automatically replayed.
- The recycle bin is stored in `.webui-trash` under the share root and uses space on the same filesystem. Its metadata is in `stateDir`. **The recycle bin is not a backup.** Stop the service and other writers, then consistently back up shared files, the hidden recycle bin, and the state database together.
- Text is not executed as a web page. Markdown previews do not execute raw HTML, and external images are not loaded automatically. Session cookies use HttpOnly and SameSite=Strict; enable Secure for HTTPS.
- **Rolling back a NixOS generation does not roll back file deletions, overwrites, or database changes.**
