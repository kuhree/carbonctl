# carbonctl as a sandboxed service: it listens on loopback for MCP clients
# on this machine and talks only to the printer.
self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.carbonctl;
  url = "http://${cfg.printer}";
  state = "/var/lib/carbonctl";
in
{
  options.services.carbonctl = {
    enable = lib.mkEnableOption "carbonctl, an MCP server for an OpenCentauri printer";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "carbonctl.packages.\${system}.default";
      description = "The carbonctl package.";
    };
    printer = lib.mkOption {
      type = lib.types.str;
      example = "192.168.1.50";
      description = "The printer's address. The service may reach nothing else off this machine.";
    };
    port = lib.mkOption {
      type = lib.types.port;
      default = 9086;
      description = "Loopback port MCP clients connect to.";
    };
    snapshotRetention = lib.mkOption {
      type = lib.types.str;
      default = "72h";
      description = "How long camera snapshots are kept (a Go duration).";
    };
  };

  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ cfg.package ];

    systemd.services.carbonctl = {
      description = "carbonctl, MCP server for an OpenCentauri printer";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = lib.escapeShellArgs [
          (lib.getExe cfg.package)
          "--listen"
          "127.0.0.1:${toString cfg.port}"
          "--printer-url"
          "ws://${cfg.printer}:3030/websocket"
          "--camera-url"
          "${url}:3031/video"
          "--history-url"
          "${url}/api/v1/print-tasks"
          "--state"
          "${state}/state.json"
          "--snapshot-dir"
          "${state}/snapshots"
          "--snapshot-retention"
          cfg.snapshotRetention
        ];
        DynamicUser = true;
        StateDirectory = "carbonctl";
        Restart = "on-failure";
        RestartSec = 5;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateDevices = true;
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
        ];
        # Its clients on this machine and the printer, nothing else.
        IPAddressDeny = "any";
        IPAddressAllow = [
          "localhost"
          cfg.printer
        ];
      };
    };
  };
}
