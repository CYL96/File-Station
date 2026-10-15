{ config, lib, pkgs, utils, ... }:
let
  cfg = config.services.file-station;
  inherit (lib) mkEnableOption mkIf mkOption types;
  absolutePath = types.strMatching "/.*";
  shares = lib.mapAttrsToList (id: share: {
    inherit id;
    inherit (share) name path readOnly;
  }) cfg.shares;
  sharePaths = map (share: share.path) shares;
  quotePath = path: lib.replaceStrings [ "%" ] [ "%%" ] (builtins.toJSON path);
  credentialPath = "/run/credentials/file-station.service/password";
  runtimeConfig = pkgs.writeText "file-station-config.json" (builtins.toJSON {
    inherit (cfg) ip port stateDir secureCookies;
    inherit shares;
    umask = "0077";
    # systemd loads the root-readable secret into a service-private credential.
    passwordFile = credentialPath;
    # Protect the original path from sharing without reading it as the service user.
    passwordSourceFile = cfg.passwordFile;
  });
  contains = root: path: root == path || lib.hasPrefix (root + "/") path;
  overlaps = first: second: contains first second || contains second first;
  normalized = path:
    path != "/" && !lib.hasSuffix "/" path
    && lib.all (part: part != "" && part != "." && part != "..")
      (lib.drop 1 (lib.splitString "/" path));
in
{
  options.services.file-station = {
    enable = mkEnableOption "the single-administrator File Station";

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ./package.nix { };
      defaultText = lib.literalExpression "pkgs.callPackage ./package.nix { }";
      description = "Package providing the file-station executable and frontend assets.";
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
      default = "/var/lib/file-station";
      description = ''
        Private persistent state directory, created with mode 0700 and owned
        by the configured user and group. Keep it outside every share. The
        SQLite share configuration here survives deployments and restarts.
        Set this explicitly to the old state directory when migrating.
      '';
    };

    passwordFile = mkOption {
      type = absolutePath;
      example = "/run/secrets/file-station-password";
      description = ''
        Absolute runtime path to a plaintext administrator password containing
        at least 6 bytes (12 or more recommended). Supply a string, not a Nix
        path literal, and do not use writeText or another store-producing
        function. systemd reads it through LoadCredential, so it may remain
        root-owned 0600. Restart file-station.service after password rotation.
      '';
    };

    secureCookies = mkOption {
      type = types.bool;
      default = false;
      description = "Set the Secure cookie flag. Enable when accessing the service through HTTPS.";
    };

    user = mkOption {
      type = types.str;
      default = "file-station";
      description = ''
        Non-root service user. The default dedicated system user is created
        automatically. Any other user must already be declared in users.users;
        its account settings and primary group are not changed by this module.
      '';
    };

    group = mkOption {
      type = types.str;
      default = "file-station";
      description = ''
        Primary group of the service process and owner group of private state.
        The group is created if needed, including when using a custom name.
      '';
    };

    supplementaryGroups = mkOption {
      type = types.listOf types.str;
      default = [ ];
      example = [ "documents" ];
      description = ''
        Existing groups granting the service process access to directories.
        The process UID, groups, filesystem permissions, ACLs and mount options
        determine which directories can be browsed and shared. This module
        never recursively changes ownership or permissions of shared data.
      '';
    };

    shares = mkOption {
      default = { };
      description = ''
        Optional initial shared-directory import. By default no directories
        are shared; add them in the UI. Attribute names become stable API IDs.
        Import occurs once per state database, even when empty; deployments
        never reconcile or overwrite saved shares. Paths must exist and be
        accessible to the service user, must not overlap one another, and
        must not overlap private state or the password file.
      '';
      type = types.attrsOf (types.submodule ({ name, ... }: {
        options = {
          name = mkOption {
            type = types.str;
            default = name;
            description = "Display name in the File Station sidebar.";
          };
          path = mkOption {
            type = absolutePath;
            example = "/srv/documents";
            description = "Existing absolute directory to expose through File Station.";
          };
          readOnly = mkOption {
            type = types.bool;
            default = false;
            description = "Disallow mutations through the API; use mount permissions or ACLs for OS-level protection.";
          };
        };
      }));
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.user != "root" && builtins.hasAttr cfg.user config.users.users
          && (config.users.users.${cfg.user}.uid or null) != 0;
        message = "services.file-station.user must name a declared non-root user.";
      }
      {
        assertion = !lib.hasPrefix "/nix/store/" cfg.passwordFile;
        message = "services.file-station.passwordFile must be a runtime secret, not a Nix store path.";
      }
      {
        assertion = lib.all normalized ([ cfg.stateDir cfg.passwordFile ] ++ sharePaths);
        message = "services.file-station stateDir, passwordFile and bootstrap share paths must be normalized absolute paths other than /.";
      }
      {
        assertion = lib.all (path:
          !overlaps cfg.stateDir path
          && !overlaps cfg.passwordFile path
          && !overlaps credentialPath path
        ) sharePaths;
        message = "services.file-station bootstrap shares must not overlap stateDir, passwordFile or the service password credential.";
      }
      {
        assertion = lib.all (id: builtins.match "[A-Za-z0-9_-]+" id != null) (builtins.attrNames cfg.shares);
        message = "services.file-station share IDs may contain only letters, digits, underscores and hyphens.";
      }
      {
        assertion = lib.all (first:
          lib.all (second: first.id == second.id || !overlaps first.path second.path) shares
        ) shares;
        message = "services.file-station share paths must not overlap one another.";
      }
    ];

    users.users = mkIf (cfg.user == "file-station") {
      file-station = {
        isSystemUser = true;
        group = cfg.group;
        description = "File Station service user";
      };
    };
    users.groups.${cfg.group} = { };

    systemd.tmpfiles.settings."file-station".${cfg.stateDir} = {
      d = {
        mode = "0700";
        user = cfg.user;
        group = cfg.group;
      };
      Z = {
        user = cfg.user;
        group = cfg.group;
      };
    };

    systemd.services.file-station = {
      description = "File Station";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network.target" ];
      after = [ "network.target" "systemd-tmpfiles-setup.service" ];
      unitConfig.RequiresMountsFor = map quotePath (lib.unique (
        [ cfg.stateDir cfg.passwordFile ] ++ sharePaths
      ));
      restartTriggers = [ runtimeConfig ];

      serviceConfig = {
        Type = "simple";
        User = cfg.user;
        Group = cfg.group;
        SupplementaryGroups = cfg.supplementaryGroups;
        ExecStart = utils.escapeSystemdExecArgs [
          (lib.getExe cfg.package) "-config" runtimeConfig
        ];
        LoadCredential = quotePath "password:${cfg.passwordFile}";
        WorkingDirectory = cfg.stateDir;
        Restart = "on-failure";
        RestartSec = 3;
        UMask = "0077";

        # No filesystem remapping: actual service-user permissions govern shares.
        NoNewPrivileges = true;
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
        # A reused UID or group may own IPC objects unrelated to this service.
        RemoveIPC = false;
      };
    };
  };
}
