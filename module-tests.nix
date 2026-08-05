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
  # The shared hermes-agent persona (nicknamed "neo") is one flat module, not
  # per-profile directories, so read its real wiring straight off the
  # evaluated oracle configuration instead of importing a standalone config.
  hermes = configuration.services.hermes-agent;
  soul = builtins.readFile ../../nix/modules/nixos/services/hermes-agent/SOUL.md;
  toolAllowlist = hermes.settings.mcp_servers.carbonctl.tools.include;
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
  service.environment.CARBONCTL_TELEGRAM_TOKEN_FILE
  == configuration.age.secrets.carbonctl-telegram-bot-token.path;
assert
  service.environment.CARBONCTL_TELEGRAM_CHAT_FILE
  == configuration.age.secrets.carbonctl-telegram-chat-id.path;
assert serviceConfig.NoNewPrivileges;
assert serviceConfig.PrivateDevices;
assert serviceConfig.ProtectSystem == "strict";
assert serviceConfig.ProtectHome;
assert
  serviceConfig.RestrictAddressFamilies == [
    "AF_INET"
    "AF_INET6"
  ];
assert configuration.age.secrets ? carbonctl-telegram-bot-token;
assert configuration.age.secrets ? carbonctl-telegram-chat-id;
assert lib.hasInfix "carbonctl-telegram-bot-token" (
  toString configuration.age.secrets.carbonctl-telegram-bot-token.file
);
assert lib.hasInfix "carbonctl-telegram-chat-id" (
  toString configuration.age.secrets.carbonctl-telegram-chat-id.file
);
assert !(lib.hasInfix "jerry-telegram" (toString configuration.age.secrets.carbonctl-telegram-bot-token.file));
assert configuration.age.secrets ? hermes-agent-env;
assert !(hermes.settings.mcp_servers ? jerry);
assert hermes.environmentFiles == [ configuration.age.secrets.hermes-agent-env.path ];
assert !(configuration.systemd.services ? hermes-agent-env);
assert !lib.any (rule: lib.hasInfix "/profiles/carbon/" rule) configuration.systemd.tmpfiles.rules;
assert !(configuration.systemd.services ? hermes-carbonctl-gateway);
assert !(configuration.services.nginx.virtualHosts ? "carbonctl.kuhree.com");
assert !(configuration.fleet.inventory.services ? carbonctl);
assert toolAllowlist == expectedTools;
assert hermes.settings.mcp_servers.carbonctl.timeout == 330;
assert hermes.settings.mcp_servers.carbonctl.elicitation.enabled;
assert hermes.settings.mcp_servers.carbonctl.elicitation.timeout == 300;
assert lib.hasInfix "For printer work" soul;
assert lib.hasInfix "Pause and resume require an explicit request" soul;
assert lib.hasInfix "Stop is always two-phase" soul;
assert hasAssertion "must bind only to an IP loopback";
assert hasAssertion "requires printer.address";
assert hasAssertion "requires exactly one Telegram bot token source or file";
assert hasAssertion "requires exactly one authorized Telegram chat source or file";
assert rejectsPublicBind;
pkgs.runCommand "carbonctl-nixos-evaluation-tests" { } ''
  touch "$out"
''
