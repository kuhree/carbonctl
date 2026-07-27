{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.fleet.node.services.carbonctl;
  listen = "${cfg.bind}:${toString cfg.port}";
  stateFile = "${cfg.stateDirectory}/state.json";
  snapshotDirectory = "${cfg.stateDirectory}/snapshots";
  printerBaseURL = "http://${cfg.printer.address}";
in
{
  options.fleet.node.services.carbonctl = {
    enable = lib.mkEnableOption "the carbonctl OpenCentauri MCP bridge";
    package = lib.mkPackageOption pkgs "carbonctl" { };
    displayName = lib.mkOption {
      type = lib.types.str;
      default = "Centauri Carbon";
      description = "Human-readable name of the single managed printer.";
    };
    printer.address = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "192.0.2.10";
      description = "LAN address of the single managed OpenCentauri printer.";
    };
    bind = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1";
      description = "Loopback address for MCP and health HTTP endpoints.";
    };
    port = lib.mkOption {
      type = lib.types.port;
      default = 9086;
      description = "Loopback port for MCP and health HTTP endpoints.";
    };
    stateDirectory = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/carbonctl";
      description = "Directory containing atomic state and retained snapshots.";
    };
    snapshotRetention = lib.mkOption {
      type = lib.types.str;
      default = "72h";
      description = "Go duration for retained snapshot history.";
    };
    telegram = {
      botTokenSource = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        description = "Ragenix source payload for the dedicated Telegram bot token.";
      };
      authorizedChatSource = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        description = "Ragenix source payload for the sole authorized Telegram chat ID.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.printer.address != "";
        message = "carbonctl requires printer.address";
      }
      {
        assertion = cfg.bind == "127.0.0.1";
        message = "carbonctl must bind only to an IP loopback address";
      }
      {
        assertion = cfg.telegram.botTokenSource != null;
        message = "carbonctl requires a ragenix Telegram bot token source";
      }
      {
        assertion = cfg.telegram.authorizedChatSource != null;
        message = "carbonctl requires a ragenix authorized Telegram chat source";
      }
    ];

    users.groups.carbonctl = { };
    users.users.carbonctl = {
      isSystemUser = true;
      group = "carbonctl";
      description = "carbonctl service account";
    };

    age.secrets = {
      carbonctl-telegram-bot-token = {
        file = cfg.telegram.botTokenSource;
        owner = config.services.hermes-agent.user;
        group = "carbonctl";
        mode = "0440";
      };
      carbonctl-telegram-chat-id = {
        file = cfg.telegram.authorizedChatSource;
        owner = config.services.hermes-agent.user;
        group = "carbonctl";
        mode = "0440";
      };
    };

    environment.systemPackages = [ cfg.package ];

    systemd.services.carbonctl = {
      description = "${cfg.displayName} MCP and Telegram bridge";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      unitConfig = {
        StartLimitIntervalSec = "5min";
        StartLimitBurst = 5;
      };
      environment = {
        CARBONCTL_TELEGRAM_TOKEN_FILE = config.age.secrets.carbonctl-telegram-bot-token.path;
        CARBONCTL_TELEGRAM_CHAT_FILE = config.age.secrets.carbonctl-telegram-chat-id.path;
      };
      serviceConfig = {
        User = "carbonctl";
        Group = "carbonctl";
        ExecStart = lib.concatStringsSep " " [
          (lib.getExe cfg.package)
          "--listen"
          (lib.escapeShellArg listen)
          "--printer-url"
          (lib.escapeShellArg "ws://${cfg.printer.address}:3030/websocket")
          "--camera-url"
          (lib.escapeShellArg "${printerBaseURL}:3031/video")
          "--history-url"
          (lib.escapeShellArg "${printerBaseURL}/api/v1/print-tasks")
          "--state"
          (lib.escapeShellArg stateFile)
          "--snapshot-dir"
          (lib.escapeShellArg snapshotDirectory)
          "--snapshot-retention"
          (lib.escapeShellArg cfg.snapshotRetention)
        ];
        Restart = "on-failure";
        RestartSec = 5;
        TimeoutStartSec = "30s";
        TimeoutStopSec = "30s";
        StateDirectory = lib.mkIf (cfg.stateDirectory == "/var/lib/carbonctl") "carbonctl";
        StateDirectoryMode = "0750";
        UMask = "0027";
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictRealtime = true;
        RestrictNamespaces = true;
        SystemCallArchitectures = "native";
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
        ];
        ReadWritePaths = [ cfg.stateDirectory ];
      };
    };
  };
}
