{ config, lib, pkgs, utils, ... }:
let
  cfg = config.services.nixos-file-station;
  inherit (lib) mkEnableOption mkIf mkOption types;
  absolutePath = types.strMatching "/.*";
  shares = lib.mapAttrsToList (id: share: {
    inherit id;
    inherit (share) name path readOnly;
  }) cfg.shares;
  sharePaths = map (share: share.path) shares;
  rootPaths = map (root: root.path) cfg.allowedRoots;
  writableRoots = lib.filter (root: !root.readOnly) cfg.allowedRoots;
  readOnlyRoots = lib.filter (root: root.readOnly) cfg.allowedRoots;
  quotePath = path: lib.replaceStrings [ "%" ] [ "%%" ] (builtins.toJSON path);
  runtimeConfig = pkgs.writeText "nixos-file-station-config.json" (builtins.toJSON {
    inherit (cfg) ip port stateDir secureCookies allowedRoots;
    inherit shares;
    # systemd loads the root-readable secret into a service-private credential.
    passwordFile = "/run/credentials/nixos-file-station.service/password";
  });
  contains = root: path: root == path || lib.hasPrefix (root + "/") path;
  overlaps = first: second: contains first second || contains second first;
  normalized = path:
    path != "/" && !lib.hasSuffix "/" path
    && lib.all (part: part != "" && part != "." && part != "..")
      (lib.drop 1 (lib.splitString "/" path));
in
{
  options.services.nixos-file-station = {
    enable = mkEnableOption "the single-administrator NixOS File Station";

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ./package.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ./package.nix { }";
      description = "Package providing the nixos-file-station executable and frontend assets.";
    };

    ip = mkOption {
      type = types.str;
      default = "127.0.0.1";
      example = "::1";
      description = ''
        IPv4 or IPv6 bind address, without brackets or port. The default is
        loopback only. This module never opens firewall ports; use an HTTPS
        reverse proxy for remote access.
      '';
    };

    port = mkOption {
      type = types.port;
      default = 8080;
      description = "HTTP listen port.";
    };

    stateDir = mkOption {
      type = absolutePath;
      default = "/var/lib/nixos-file-station";
      description = ''
        Private persistent state directory, created with mode 0700 and owned
        by nixos-file-station. Keep it outside every allowed root. The SQLite share
        configuration here survives deployment changes and service restarts.
      '';
    };

    passwordFile = mkOption {
      type = absolutePath;
      example = "/run/secrets/nixos-file-station-password";
      description = ''
        Absolute runtime path to a plaintext administrator password containing
        at least 6 bytes (12 or more recommended). Supply a string, not a Nix path literal, and do not
        use writeText or another store-producing function. The secret is read
        by systemd through LoadCredential, so it may remain root-owned 0600.
        Password rotation takes effect after restarting nixos-file-station.service.
      '';
    };

    secureCookies = mkOption {
      type = types.bool;
      default = false;
      description = "Set the Secure cookie flag. Enable when accessing the service through HTTPS.";
    };

    supplementaryGroups = mkOption {
      type = types.listOf types.str;
      default = [ ];
      example = [ "documents" ];
      description = ''
        Existing groups granting the dedicated service user access to shared
        directories. Share ownership, ACLs and mount permissions remain under
        administrator control; the module does not recursively change them.
      '';
    };

    allowedRoots = mkOption {
      type = types.listOf (types.submodule {
        options = {
          path = mkOption {
            type = absolutePath;
            example = "/srv/documents";
            description = "Existing normalized absolute directory bounding administrator-managed shares.";
          };
          readOnly = mkOption {
            type = types.bool;
            default = false;
            description = "Disallow writable shares within this root and mount the root read-only in the service sandbox.";
          };
        };
      });
      default = [ ];
      description = ''
        Deployment-controlled filesystem bounds for administrator-managed shares.
        At least one root is required; roots must not overlap one another, the
        state directory, or the password file. Paths must already exist and
        cannot traverse symlinks. Sandbox access is derived from these roots,
        not from bootstrap or saved shares.
      '';
    };

    shares = mkOption {
      default = { };
      description = ''
        Initial shared-directory import only. Attribute names become stable
        API share IDs. Import occurs once per state database, even when empty;
        subsequent deployments never reconcile or overwrite saved shares.
        Bootstrap paths must already exist, must not overlap one another, and
        must be within allowedRoots with compatible read-only permissions.
      '';
      type = types.attrsOf (types.submodule ({ name, ... }: {
        options = {
          name = mkOption {
            type = types.str;
            default = name;
            description = "Display name in the NixOS File Station sidebar.";
          };
          path = mkOption {
            type = absolutePath;
            example = "/srv/documents";
            description = "Existing absolute directory to expose through NixOS File Station.";
          };
          readOnly = mkOption {
            type = types.bool;
            default = false;
            description = "Disallow mutations through the API. OS-level read-only protection requires a readOnly allowed root.";
          };
        };
      }));
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = !lib.hasPrefix "/nix/store/" cfg.passwordFile;
        message = "services.nixos-file-station.passwordFile must be a runtime secret, not a Nix store path.";
      }
      {
        assertion = cfg.allowedRoots != [ ];
        message = "services.nixos-file-station.allowedRoots must contain at least one directory.";
      }
      {
        assertion = lib.all normalized ([ cfg.stateDir cfg.passwordFile ] ++ rootPaths ++ sharePaths);
        message = "services.nixos-file-station stateDir, passwordFile, allowed roots and bootstrap share paths must be normalized absolute paths other than /.";
      }
      {
        assertion = lib.all (valid: valid) (lib.imap0 (index: root:
          lib.all (other: !overlaps root.path other.path) (lib.drop (index + 1) cfg.allowedRoots)
        ) cfg.allowedRoots);
        message = "services.nixos-file-station.allowedRoots must not overlap one another.";
      }
      {
        assertion = lib.all (path:
          !overlaps cfg.stateDir path
          && !overlaps cfg.passwordFile path
          && !overlaps "/run/credentials/nixos-file-station.service/password" path
        ) rootPaths;
        message = "services.nixos-file-station allowed roots must not overlap stateDir, passwordFile or the service password credential.";
      }
      {
        assertion = lib.all (share: lib.any (root:
          contains root.path share.path && (!root.readOnly || share.readOnly)
        ) cfg.allowedRoots) shares;
        message = "services.nixos-file-station bootstrap shares must be within an allowed root and read-only when that root is read-only.";
      }
      {
        assertion = lib.all (id: builtins.match "[A-Za-z0-9_-]+" id != null) (builtins.attrNames cfg.shares);
        message = "services.nixos-file-station share IDs may contain only letters, digits, underscores and hyphens.";
      }
      {
        assertion = lib.all (first:
          lib.all (second: first.id == second.id || !overlaps first.path second.path) shares
        ) shares;
        message = "services.nixos-file-station share paths must not overlap one another.";
      }
    ];

    users.groups.nixos-file-station = { };
    users.users.nixos-file-station = {
      isSystemUser = true;
      group = "nixos-file-station";
      home = cfg.stateDir;
      extraGroups = cfg.supplementaryGroups;
      description = "NixOS File Station service user";
    };

    systemd.tmpfiles.settings."nixos-file-station".${cfg.stateDir}.d = {
      mode = "0700";
      user = "nixos-file-station";
      group = "nixos-file-station";
    };

    systemd.services.nixos-file-station = {
      description = "NixOS File Station";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network.target" ];
      after = [ "network.target" "systemd-tmpfiles-setup.service" ];
      unitConfig.RequiresMountsFor = map quotePath (lib.unique (
        [ cfg.stateDir cfg.passwordFile ] ++ rootPaths
      ));
      restartTriggers = [ runtimeConfig ];

      serviceConfig = {
        Type = "simple";
        User = "nixos-file-station";
        Group = "nixos-file-station";
        SupplementaryGroups = cfg.supplementaryGroups;
        ExecStart = utils.escapeSystemdExecArgs [
          (lib.getExe cfg.package) "-config" runtimeConfig
        ];
        LoadCredential = quotePath "password:${cfg.passwordFile}";
        WorkingDirectory = cfg.stateDir;
        Restart = "on-failure";
        RestartSec = 3;
        UMask = "0077";

        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = "read-only";
        ReadWritePaths = map quotePath ([ cfg.stateDir ] ++ map (root: root.path) writableRoots);
        ReadOnlyPaths = map (root: quotePath root.path) readOnlyRoots;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        ProcSubset = "pid";
        CapabilityBoundingSet = "";
        AmbientCapabilities = "";
        RestrictAddressFamilies = [ "AF_UNIX" "AF_INET" "AF_INET6" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RemoveIPC = true;
      };
    };
  };
}
