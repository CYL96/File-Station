# File Station

[简体中文](README.md) | **English**

A single-administrator web file manager for Linux, Docker, and NixOS. Files stay in their original directories; SQLite persists settings, shares, tasks, and sessions. Refresh to see changes made on the host or through Samba.

- Multiple shares, read-only shares, search, sorting, and list/grid views.
- Upload, download, create, rename, copy, move, conflict handling, and a recycle bin.
- Background tasks, progress, cancellation, and retries after failure.
- Image previews, MP4/WebM playback, UTF-8 text editing, and Markdown source/preview/visual editing.
- Chinese and English interfaces, with configurable date and permission display formats.

**Choose an installation method:** [Docker / Compose](#docker--compose) · [Prebuilt binaries](#prebuilt-binaries) · [Build from source](#build-from-source) · [Nix / NixOS](#nix--nixos)

**Run only as a non-root user.** All file operations use the Linux identity of the service process; there is no multi-user identity mapping or privilege escalation. By default, the first startup has no shares: sign in and add existing directories. With Docker, mount the data first, then add its container-side path.

![File Station file manager in Chinese, showing shared-directory navigation, a file list, and the file operations toolbar](docs/screenshots/files-list-zh.png)

The screenshot uses demo data and the Chinese interface. A fresh installation still requires you to add your own shares.

## Installation and deployment

### Docker / Compose

Use GHCR release images by default. Only Docker Engine and the Compose plugin are required; **no repository clone, Go, or Node.js is needed**. Choose a deployment directory, for example:

```sh
mkdir -p "$HOME/file-station"
cd "$HOME/file-station"
```

Create `compose.yaml` in that directory:

```yaml
name: file-station

services:
  file-station:
    image: ghcr.io/cyl96/file-station:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      PUID: "${PUID-1000}"
      PGID: "${PGID-1000}"
      UMASK: "${UMASK-0022}"
      PASSWORD: "${PASSWORD-}"
      ConfigPath: "${ConfigPath-/var/lib/file-station}"
    volumes:
      - "./state:${ConfigPath-/var/lib/file-station}"
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
    security_opt:
      - no-new-privileges:true
```

Release images support `linux/amd64` and `linux/arm64`; Docker automatically selects the host architecture. `latest` tracks the newest stable release. To pin a version, change `image` to `ghcr.io/cyl96/file-station:X.Y.Z`, without a `v` in the tag. For example, `ghcr.io/cyl96/file-station:1.0.0` **only illustrates the format; replace it with an actually published version**. Check the image tag for the corresponding [Release](https://github.com/CYL96/File-Station/releases) before use.

Create a private `.env` in the same directory (never commit it) with these contents:

```dotenv
PUID=1000
PGID=1000
UMASK='0022'
PASSWORD='replace-with-your-own-long-password'
ConfigPath='/var/lib/file-station'
```

After creating the file, restrict its permissions:

```sh
chmod 600 .env
```

Set `PUID` / `PGID` to a non-root UID/GID with access to your data (use `id -u` / `id -g` to check), and replace the password. Passwords must be at least 6 bytes; 12 or more is recommended. Single-quote the password in `.env` to avoid `$` interpolation; a `$` written directly in Compose YAML must instead be escaped as `$$`.

```sh
mkdir -p state
docker compose pull
docker compose up -d
```

Open <http://127.0.0.1:8080>. The application defaults to listening on `0.0.0.0:8080` inside the container, while the example above publishes only to the host loopback address. `./state` is persistently mounted at `ConfigPath`; setting it to `/config`, for example, automatically changes the mount to `./state:/config`. **`ConfigPath` is the state directory, not a config.json path.** External access requires explicitly changing the port mapping and firewall; see [Passwords and networking](#passwords-and-networking).

Add shared-data mounts to the existing `volumes` in `compose.yaml`, for example:

```yaml
      - "/srv/documents:/mnt/documents"
      # - "/srv/shared archive:/mnt/archive:ro"
```

After recreating the container, add `/mnt/documents` in the UI, not the host path. Unmounted host directories are invisible, and mounting a directory does not automatically create a share.

#### Container identity and permissions

- The root entrypoint defaults to `1000:1000`, prepares only the state directory itself, then drops privileges with `su-exec`. It does not recursively change state contents or shared data. `PUID` / `PGID` accept only decimal integers from `1` to `4294967294`, without leading zeros; empty values are invalid.
- No `user:` setting is needed by default. With an explicit non-root `--user UID:GID`, unset `PUID` / `PGID` inherit that identity; explicitly conflicting values cause an error. Prepare state-directory ownership beforehand: the entrypoint does not elevate privileges. The example above explicitly passes `PUID` / `PGID` by default, so adjust them to match when using `user:`.
- Keep the example's `cap_drop: ALL`, only the added `CHOWN` / `SETUID` / `SETGID` capabilities, and `no-new-privileges`. `su-exec` clears supplementary groups, so do not rely on `group_add`; grant access through the actual UID, primary group, or ACLs. Rootless Docker also requires accounting for host UID mappings.
- **When changing UID/GID for existing state, stop the service, back up, and migrate ownership of the entire state directory while preserving contents and permissions. Do not delete the database or recursively chown shared data.**

#### Container JSON mode

To customize the listener, enable secure cookies, or use a password file, use the JSON format in [Configuration reference](#configuration-reference), set `ip` to `"0.0.0.0"`, and use container-side paths throughout. Align JSON `stateDir`, `ConfigPath`, and the state mount target, then merge the following into the Compose service:

```yaml
    volumes:
      - './state:${ConfigPath-/var/lib/file-station}'
      - "./config.json:/etc/file-station/config.json:ro"
    command: ["-config", "/etc/file-station/config.json", "-static", "/usr/share/file-station"]
```

Merge with the existing `volumes`; do not declare that key twice. JSON can use `password`, or `passwordFile` with an additional read-only password-file mount; exactly one source must be nonempty. Configuration and passwords must be readable by the runtime UID and remain outside shares. Keep `-static` when overriding the image command.

In JSON mode, Go ignores `ConfigPath`, `PASSWORD`, and `UMASK`, but the container entrypoint still validates and prepares `ConfigPath` without reading JSON. If it differs from `stateDir`, you must provide persistent storage and permissions for the actual state directory yourself. Changing only the state directory does not require JSON mode.

#### Optional: build from local source

The repository's `compose.yaml` still uses `build: .` and `image: file-station:local`. To build locally, obtain the source and, from the repository root, create and secure the private `.env` as described above (including `chmod 600 .env`), then run:

```sh
mkdir -p state
docker compose up -d --build
```

To switch an existing repository deployment to release images, remove `build` from the Compose service and replace `image` with `ghcr.io/cyl96/file-station:latest` or an actually published fixed version tag. Retain the original `.env`, state directory, mounts, identity, and security settings, and follow the release-image steps under [Regular upgrades](#regular-upgrades) below. **Do not reset state or delete the database.**

### Prebuilt binaries

Download the archive for your Linux architecture (amd64 or arm64) and `SHA256SUMS` from [Releases](https://github.com/CYL96/File-Station/releases). **Go, Node.js, npm, and Nix are not required to run it.** GitHub's automatically generated Source code archives are not binary installers.

First prepare configuration, a private password, and a writable state directory under [Local configuration and password](#local-configuration-and-password). Then verify, extract, and start from the download directory. The following uses amd64 `v1.0.0` as an example; substitute your downloaded version. `--ignore-missing` allows downloading just one architecture:

```sh
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf file-station-v1.0.0-linux-amd64.tar.gz
cd file-station-v1.0.0-linux-amd64
./file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

The archive includes the executable and matching `web/dist`. Run as a non-root user and open <http://127.0.0.1:8080>. When starting from another working directory, use the absolute path to the extracted `web/dist` for `-static`.

### Build from source

Requires Linux, Go 1.26, Node.js 24, and npm; alternatively, first run `nix develop path:.`. Build from the repository root:

```sh
npm --prefix web ci
npm --prefix web run build
go build -o /tmp/file-station ./cmd/file-station
```

Prepare configuration under [Local configuration and password](#local-configuration-and-password), then start as a non-root user from the repository root:

```sh
/tmp/file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

Open <http://127.0.0.1:8080>. Keep the executable and matching frontend when deploying; use the correct `-static` path if starting from another directory.

### Nix / NixOS

Build the package from the repository root, prepare configuration under [Local configuration and password](#local-configuration-and-password), then run it. The wrapper includes the frontend path, so `-static` is unnecessary:

```sh
nix build path:.#file-station
./result/bin/file-station -config "$HOME/.config/file-station/config.json"
```

`default` and `file-station` refer to the same package, supporting `x86_64-linux` and `aarch64-linux`.

#### NixOS service

Merge this input into your existing system flake and add `inputs.file-station.nixosModules.default` to the host's `modules` list:

```nix
{
  inputs.file-station.url = "github:CYL96/File-Station";
}
```

Add the service configuration to the same `modules` list (within the `outputs = inputs@{ ... }: ...` scope, using its outer `inputs`):

```nix
{ pkgs, ... }: {
  services.file-station = {
    enable = true;
    package = inputs.file-station.packages.${pkgs.stdenv.hostPlatform.system}.default;
    ip = "127.0.0.1";
    port = 8080;
    passwordFile = "/run/secrets/file-station-password";
    # stateDir = "/var/lib/file-station";
    # supplementaryGroups = [ "documents" ];
    # secureCookies = true; # HTTPS only
  };
}
```

- The module generates JSON; no handwritten local configuration is needed. Prepare a runtime password file before deploying; it may be `root:root 0600`. systemd `LoadCredential` passes it to the service and the original source path is protected. **Use an absolute runtime path string, never a Nix path literal, `writeText`, or another store file for the password.**
- By default, the module creates a dedicated `file-station` user/group and no shares. You can set `user` / `group`; custom users must already be declared in `users.users`, and the module creates the group. `supplementaryGroups` affects only the service process; reusing an account loses the isolation of a dedicated UID.
- The module fixes JSON `umask: "0077"` and systemd `UMask=0077`. Ordinary data access follows service-account permissions, with no separate path allowlist and no recursive changes to shared directories. The module prepares ownership/group of private state and its contents while preserving existing state-file permissions.
- Arrange network-mount ordering yourself, for example with `systemd.services.file-station.unitConfig.RequiresMountsFor = [ "/mnt/documents" ];`. The module does not open the firewall automatically.

```sh
sudo nixos-rebuild switch --flake /path/to/system-flake#myhost
systemctl status file-station
journalctl -u file-station
```

#### Cachix binary cache

The public cache is [cyl96-file-station](https://app.cachix.org/cache/cyl96-file-station). Add this to your NixOS configuration and rebuild. Downloads require no token; trusting the public key means trusting the maintainer's build outputs:

```nix
{
  nix.settings = {
    extra-substituters = [ "https://cyl96-file-station.cachix.org" ];
    extra-trusted-public-keys = [ "cyl96-file-station.cachix.org-1:DVc1WCQcvfD3w6trxmPHxDZRo49HxAnojhMoslE4Bio=" ];
  };
}
```

Cache matches require exact store paths. Use `inputs.file-station.packages` as above, retain the project's locked nixpkgs without setting `file-station.inputs.nixpkgs.follows`, and lock to an uploaded commit. Source or build-option overrides may miss the cache. Use `nix build --dry-run github:CYL96/File-Station#default` to inspect the plan; only actual logs showing paths copied from the Cachix URL establish a hit. Cache misses build locally. Keep the official `https://cache.nixos.org` for shared dependencies; `extra-substituters` does not replace it.

## Configuration

### Local configuration and password

These steps are shared by prebuilt binaries, source builds, and directly running the Nix package. Docker defaults to `.env`; the NixOS module generates configuration itself. Prepare directories and a password as the non-root account that will run the service. The password commands below use Bash:

```sh
mkdir -p "$HOME/.config/file-station" "$HOME/.local/state/file-station"
chmod 700 "$HOME/.config/file-station" "$HOME/.local/state/file-station"
read -rs -p 'Administrator password: ' password
printf '\n'
(umask 077; printf '%s' "$password" > "$HOME/.config/file-station/password")
unset password
```

Passwords must be at least 6 bytes; 12 or more is recommended. Create `$HOME/.config/file-station/config.json`, replacing `/absolute/home` with the actual absolute path (JSON does not expand environment variables):

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
```

Keep configuration and passwords outside all shares. You may replace `passwordFile` with JSON `password`, but exactly one password source must be nonempty. Never use a documentation example as your real password.

### Configuration reference

The two sources **are not combined**. Restart after changing startup configuration; saving shares through the UI takes effect immediately.

| Startup mode | Configuration source and defaults |
| --- | --- |
| `-config /path/config.json` | Built-in defaults + strict JSON; completely ignores `ConfigPath`, `PASSWORD`, and `UMASK`, without using them to fill missing fields. Unknown fields, invalid JSON, or extra JSON values prevent startup |
| Omit `-config` or pass an empty value | Reads `PASSWORD`, `UMASK`, and `ConfigPath`; listens on `0.0.0.0:8080`, starts with no shares, and uses `secureCookies=false` |

`-config` does not select a file through an environment variable. `-static` independently selects the frontend directory, defaults to `web/dist`, and is not a JSON field. Docker and Nix already specify the packaged path.

| JSON field | Default / description |
| --- | --- |
| `ip` / `port` | `127.0.0.1` / `8080`; IP without a port or IPv6 brackets, and port from `1–65535` |
| `stateDir` | `/var/lib/file-station`; a private, persistent, nonempty absolute directory, owned by the runtime UID with mode `0700`. Regular users usually need to select a writable path |
| `password` / `passwordFile` | Exactly one must be nonempty; at least 6 bytes, with 12 or more recommended. File paths must be absolute; only trailing newlines are stripped, preserving other whitespace |
| `umask` | `"0022"`; a 3- or 4-digit octal string no greater than `0777`, not a number or empty string |
| `secureCookies` | `false`; set to `true` for HTTPS, not plain HTTP |
| `shares` | `[]`; imported only on first initialization, then managed in `state.sqlite` |
| `passwordSourceFile` | Empty; additionally protects the original password source's absolute path and resolvable real path, without reading it or using it for authentication. Set automatically by NixOS credential deployment; applies on every startup and is not cleared when changing password sources. Remove or empty it explicitly to clear it |

| Environment variable | Default / description |
| --- | --- |
| `PASSWORD` | No default; required in environment-variable mode, with the length rules above. Ignored in JSON mode |
| `UMASK` | `0022`, with the same format as JSON `umask`; explicitly empty values are invalid. Ignored in JSON mode |
| `ConfigPath` | `/var/lib/file-station`, case-sensitive; the state directory, not a JSON path. Must be a nonempty absolute directory that does not normalize or resolve to `/`; containers also require that the directory itself is not a symlink. Go JSON mode ignores it, but the container entrypoint still uses it |
| `PUID` / `PGID` | Container identity only, not application configuration; see [Container identity and permissions](#container-identity-and-permissions) |

Ordinary paths may contain spaces. The Compose example above passes only these five variables, not other host variables automatically, and offers no short-name or legacy aliases.

Optional initial JSON import: `"shares": [{"id":"documents","name":"文档","path":"/srv/documents","readOnly":false}]`. `name` and `path` are required, `id` may be omitted, and `readOnly` defaults to `false`. The NixOS equivalent is `shares.documents = { name = "文档"; path = "/srv/documents"; readOnly = false; };`, where the attribute name is the share ID. **Even an initially empty list completes initialization: later configuration changes, restarts, and generation rollbacks do not reimport shares.** Manage shares through the UI afterward.

## Usage

### First sign-in and shares

1. Open the address configured for your deployment and sign in with the administrator password.
2. In **Settings → Share settings**, add existing, accessible, non-overlapping directories. Docker requires container-side mount paths.
3. Enable read-only access as needed. Shares cannot cover state or password paths. Browsing from `/` in the directory picker does not mean `/` can be shared; choose specific data subdirectories rather than the entire home directory.

Saving takes effect immediately. Removing a share does not delete files. Share changes return 409 while file requests are being processed; affected shares cannot be modified or removed while they have active tasks. A nonempty recycle bin prevents changing the path or removing a share. Missing directories, unavailable mounts, or insufficient permissions mark the share unavailable without automatically deleting its configuration.

### Common operations and interface settings

- Browse, search, sort, and select multiple items in list or grid view, then upload, download, create, rename, copy, move, or use the recycle bin. For conflicts, skip, replace files, or keep both; same-name directories are not recursively overwritten.
- The task center shows progress and supports cancellation. Failed copy, move, and recycle-bin tasks can retry unfinished items while retaining old records. Upload retries work only on the current page: select files again after refresh or sign-out. Unfinished tasks are marked interrupted on restart and are not automatically replayed.
- Configure the interface language, date format, and permission display format; display settings do not change file contents or actual permissions.
- Images can be previewed; MP4/WebM use native browser playback without server-side transcoding. H.264 + AAC is recommended for MP4; actual support depends on the browser and operating system. Download unsupported files instead.
- Text and Markdown can be edited within the limits under [File operations and editing](#file-operations-and-editing).

## Security and limitations

### Permissions and state backups

- There is no `allowedRoots` allowlist. Access depends on the service's actual UID, primary/supplementary groups, Unix permissions, ACLs, and mount options; all parent directories must be traversable. Read-only shares are an additional restriction: use read-only mounts or ACLs for operating-system-level protection. Application checks do not replace system isolation; mount points and hard links may still expose data.
- **Preserve the entire state directory and share IDs.** Do not delete `state.sqlite` to reset settings. State must not be temporary, overlap shares, or be replaced during deployment.
- Recycle-bin contents occupy space in `.webui-trash` under each share; metadata is in the state database. Stop the service and other writers, then consistently back up shared files, hidden recycle bins, and the entire state directory together, and privately back up startup configuration and passwords. **The recycle bin is not a backup, and NixOS rollbacks do not undo file or database changes.**

### File operations and editing

- Share root paths resolve symlinks. Inside shares, symlinks are not followed: they cannot be entered, downloaded, previewed, or copied; move, rename, and recycle operations act on the link itself. Only regular files and directories are handled. The share root cannot be deleted, and ordinary APIs cannot operate on internal `.webui-*` paths.
- New files use `0666 & ~umask`, and new directories use `0777 & ~umask`; default `0022` yields `0644/0755`, while NixOS `0077` yields `0600/0700`, still subject to system ACLs and other rules. Text saves, same-filesystem moves, and recycle-bin restores preserve original permissions. Copying does not preserve ownership, ACLs, extended attributes, hard-link relationships, or original timestamps.
- Uploads and copies first write hidden staging files. Cancellation does not roll back completed items, and interruptions may leave staging files; clean them up only after confirming no tasks remain. Same-filesystem moves are atomic renames; cross-filesystem moves copy then delete and **are not an overall transaction**. Failure can leave both copies.
- Text editing is limited to existing regular UTF-8 files of at most 1 MiB without NUL bytes. Saving is unavailable in read-only shares or the recycle bin, without write permission, or for files with multiple hard links. Saves must preserve permissions, ownership/group, ACLs, and extended attributes or they are rejected; atomic replacement changes the inode and timestamps. External changes report a conflict and retain the draft, but **this is not filesystem-level CAS**. Uniform CRLF is preserved; other line endings become LF.
- Markdown previews do not execute raw HTML or automatically load external images. Visual editing reserializes Markdown; use source editing for exact preservation. Switching languages during visual editing retains the document and unsaved state, but resets undo history, the cursor, and uncommitted insertion dialogs. Finish dialog operations before switching.

### Passwords and networking

- Set password files and configuration containing passwords to `0600`, keep them outside shares, and never commit them or put them in image layers or the Nix store. Nix users must also keep configuration and passwords outside the source tree so `path:` builds do not copy them into the store, which is normally readable by all local users. Plaintext environment variables and Compose configuration can be read through `docker inspect` or by authorized host administrators. Use read-only JSON / password files to reduce environment-variable exposure.
- Sessions last 12 hours; cookies use HttpOnly and SameSite=Strict, with CSRF protection. Enable `secureCookies` behind an HTTPS proxy (the same NixOS option; Docker requires JSON mode). Enabling it over plain HTTP prevents browsers from sending the session cookie.
- Reverse proxies must preserve Host and keep the frontend and API on the same origin. Disable SSE buffering for `/api/events`, extend connection timeouts, and set upload size limits as needed. Expose only necessary ports to trusted networks and prefer HTTPS; NixOS does not open the firewall automatically.
- The built-in update check contacts GitHub from the server, checks only stable Releases, and links to the release page. It **does not download or install updates automatically**.

## Upgrades and legacy migration

### Regular upgrades

Read the target version's release notes, prepare the new version, then stop the service and consistently back up state, shared files, and hidden recycle bins, retaining configuration and passwords. Preserve the original state path, share IDs, runtime UID/GID, and necessary startup arguments. Never let old and new instances access the same state database concurrently.

| Installation method | How to update |
| --- | --- |
| Docker / Compose (release image) | Set `image` to the target release tag and run `docker compose pull`; run `docker compose stop` and complete the consistent backup described above before `docker compose up -d`. Preserve `.env`, `./state`, mounts, and runtime UID/GID |
| Docker / Compose (local-source build) | Update the local repository to the target version, preserving `.env`, `./state`, mounts, and runtime UID/GID. Stop the service and complete the consistent backup described above, then run `docker compose up -d --build` |
| Prebuilt binary | Download the target version and checksums, verify, and extract into a new directory. Stop the old process, update the command or service to use the new executable and matching `web/dist`, and start with the original configuration |
| Source build | Update source and rebuild both frontend and backend. Stop the old process, deploy matching outputs, and start with the original configuration |
| Nix package | Update source or the locked flake version, run `nix build` again, switch the actual startup path to the new package, then manually restart the service |
| NixOS service | Update the `file-station` locked version in your system flake and run `nixos-rebuild switch` |

After upgrading, reopen the page and confirm shares, settings, and files work as expected. Switching to an old program or NixOS generation does not restore files or the state database; use a consistent backup when restoration is needed.

### Legacy migration

The old `nixos-file-station` executable/package, `services.nixos-file-station`, old unit, and `FILE_STATION_*` environment variables have been removed without compatibility aliases. Use `file-station` / `services.file-station` and the configuration above. Remove old `allowedRoots` and grant access with system permissions; remove old Compose secrets or move to explicitly selected JSON mode. The repository address is `CYL96/File-Station`.

1. **Obtain or successfully build the new version first, then stop the old service and back up** the entire state, shares and recycle bins, configuration, passwords, and unit/drop-ins. Keep backups outside state and all shares. Never run old and new services against the same state database concurrently.
2. Explicitly retain the original state path (old default `/var/lib/nixos-file-station`, new default `/var/lib/file-station`): use JSON `stateDir`, Docker environment-variable `ConfigPath`, aligned configuration and mounts in container JSON mode, or NixOS `services.file-station.stateDir`. Alternatively, migrate all state while stopped and correctly adjust ownership. **Never delete and recreate the database.**
3. Preserve the original password and necessary startup settings. Move old environment passwords into JSON `password` or `passwordFile`; retain systemd credentials and `passwordSourceFile` protection as needed. Use JSON for custom listeners and similar settings; select the frontend only with `-static`. New default `0022` is less restrictive: set `0027` for the old `0640/0750` permissions (Docker uses `UMASK`); NixOS remains `0077`.
4. For NixOS, update the module name/options and rebuild. For old user services, first prepare the new Nix package or identify the path to an already installed new version, and inspect the original setup with `systemctl --user cat nixos-file-station.service`. After backing up, create `~/.config/systemd/user/file-station.service` (create its parent directory if necessary), retaining credentials, mount dependencies, and custom settings. Back up and merge any existing new unit/drop-ins rather than overwriting them. Set the new unit's startup line to `ExecStart="/absolute/path/to/new-package/bin/file-station" -config "/absolute/path/to/existing-config.json"`, replacing both paths with the real absolute paths to the new executable and original configuration. Retain quotes around both the executable and configuration paths to support spaces; the Nix package needs no separate `-static`. Then run in order:

   ```sh
   systemctl --user stop nixos-file-station.service
   systemctl --user disable nixos-file-station.service
   systemctl --user daemon-reload
   systemctl --user enable --now file-station.service
   systemctl --user status file-station.service --no-pager
   ```

5. The session cookie has been renamed, so sign in again. Confirm the original shares, settings, and files remain, and migrate monitoring, log commands, and unit overrides. User-service logs are available with `journalctl --user -u file-station.service`.

Maintainer release guide: [RELEASING.md](https://github.com/CYL96/File-Station/blob/main/RELEASING.md).
