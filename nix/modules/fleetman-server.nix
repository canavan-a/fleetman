{ config, lib, pkgs, fleetman, system, ... }:

let
  cfg = config.services.fleetman-server;
in {
  options.services.fleetman-server = {
    enable = lib.mkEnableOption "Fleet Manager Server";

    package = lib.mkOption {
      type    = lib.types.package;
      default = fleetman.packages.${system}.fleetman-server;
      description = "The fleetman-server package to use.";
    };

    addr = lib.mkOption {
      type    = lib.types.str;
      default = ":8080";
      description = "Public listen address.";
    };

    adminAddr = lib.mkOption {
      type    = lib.types.str;
      default = "127.0.0.1:3333";
      description = "Admin listen address (localhost only). Set to empty string to disable.";
    };

    dbPath = lib.mkOption {
      type    = lib.types.str;
      default = "/var/lib/fleetman/fleetman.db";
      description = "Path to the SQLite database file.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.fleetman-server = {
      description = "Fleet Manager Server";
      after    = [ "network-online.target" ];
      wants    = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        ExecStart = lib.concatStringsSep " " (
          [ "${cfg.package}/bin/fleetman-server"
            "--addr"       cfg.addr
            "--db"         cfg.dbPath
          ] ++ lib.optional (cfg.adminAddr != "")
            "--admin-addr ${cfg.adminAddr}"
        );
        Restart         = "always";
        RestartSec      = 5;
        KillMode        = "process";
        StateDirectory  = "fleetman";
      };
    };
  };
}
