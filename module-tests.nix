{
  badConfiguration,
  configuration,
  lib,
  pkgs,
}:
let
  cfg = configuration.fleet.node.services.carbonctl;
  service = configuration.systemd.services.carbonctl;
  inherit (service) serviceConfig;
  gateway = configuration.systemd.services.hermes-carbonctl-gateway;
  profileConfig = import ../../nix/modules/nixos/services/hermes-agent/profiles/carbon/config.nix {
    workspace = "/var/lib/hermes/.hermes/profiles/carbon/workspace";
    mcpURL = "http://127.0.0.1:9086/mcp";
  };
  soul = builtins.readFile ../../nix/modules/nixos/services/hermes-agent/profiles/carbon/SOUL.md;
  toolAllowlist = profileConfig.mcp_servers.carbonctl.tools.include;
  expectedTools = [
    "carbonctl_get_status"
    "carbonctl_get_snapshot"
    "carbonctl_get_history"
    "carbonctl_pause"
    "carbonctl_resume"
    "carbonctl_prepare_stop"
    "carbonctl_confirm_stop"
  ];
  hasAssertion =
    fragment: lib.any (entry: lib.hasInfix fragment entry.message) configuration.assertions;
  rejectsPublicBind = lib.any (
    entry: !entry.assertion && lib.hasInfix "must bind only to an IP loopback" entry.message
  ) badConfiguration.assertions;
in
assert cfg.enable;
assert cfg.bind == "127.0.0.1";
assert service.wantedBy == [ "multi-user.target" ];
assert lib.hasInfix "--listen 127.0.0.1:9086" serviceConfig.ExecStart;
assert !lib.hasInfix "telegram-token-file" serviceConfig.ExecStart;
assert !lib.hasInfix "telegram-chat-file" serviceConfig.ExecStart;
assert !lib.hasInfix "TELEGRAM_BOT_TOKEN=" serviceConfig.ExecStart;
assert
  service.environment.CARBONCTL_TELEGRAM_TOKEN_FILE == "/run/agenix/carbonctl-telegram-bot-token";
assert service.environment.CARBONCTL_TELEGRAM_CHAT_FILE == "/run/agenix/carbonctl-telegram-chat-id";
assert serviceConfig.NoNewPrivileges;
assert serviceConfig.PrivateDevices;
assert serviceConfig.ProtectSystem == "strict";
assert serviceConfig.ProtectHome;
assert
  serviceConfig.RestrictAddressFamilies == [
    "AF_INET"
    "AF_INET6"
  ];
assert configuration.age.secrets.carbonctl-telegram-bot-token.owner == "hermes";
assert configuration.age.secrets.carbonctl-telegram-bot-token.group == "carbonctl";
assert configuration.age.secrets.carbonctl-telegram-chat-id.mode == "0440";
assert gateway.requires == [ "carbonctl.service" ];
assert
  gateway.serviceConfig.EnvironmentFile == [
    "/var/lib/hermes/.hermes/.env"
    "/run/agenix/carbonctl-telegram-bot-token"
    "/run/agenix/carbonctl-telegram-chat-id"
  ];
assert lib.any (
  rule: lib.hasInfix "/profiles/carbon/config.yaml" rule
) configuration.systemd.tmpfiles.rules;
assert lib.any (
  rule: lib.hasInfix "/profiles/carbon/SOUL.md" rule
) configuration.systemd.tmpfiles.rules;
assert !(configuration.services.nginx.virtualHosts ? "carbonctl.kuhree.com");
assert !(configuration.fleet.inventory.services ? carbonctl);
assert toolAllowlist == expectedTools;
assert profileConfig.mcp_servers.carbonctl.timeout == 330;
assert profileConfig.mcp_servers.carbonctl.elicitation.enabled;
assert profileConfig.mcp_servers.carbonctl.elicitation.timeout == 300;
assert profileConfig.toolsets == [ "carbonctl" ];
assert
  profileConfig.agent.disabled_toolsets == [
    "browser"
    "cronjob"
    "file"
    "messaging"
    "terminal"
    "web"
  ];
assert lib.hasInfix "natural-language-first" soul;
assert lib.hasInfix "`/status`, `/snapshot`, `/history`, and `/help`" soul;
assert lib.hasInfix "`/pause` and `/resume`" soul;
assert lib.hasInfix "`/stop` never stops immediately" soul;
assert hasAssertion "must bind only to an IP loopback";
assert hasAssertion "requires printer.address";
assert rejectsPublicBind;
pkgs.runCommand "carbonctl-nixos-evaluation-tests" { } ''
  touch "$out"
''
