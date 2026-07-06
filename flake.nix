{
  description = "Fleetman — fleet manager server, agent, and control CLI";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    let
      version = self.shortRev or self.dirtyShortRev or "dev";

      # Per-system packages (binaries)
      perSystem = flake-utils.lib.eachDefaultSystem (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          mkFleetPkg = { pname, subPackage }: pkgs.buildGoModule {
            inherit pname version;
            src = ./.;
            subPackages = [ subPackage ];
            vendorHash = null;
            env.CGO_ENABLED = "0";
            ldflags = [ "-X main.Version=${version}" ];
          };
        in
        {
          packages = {
            default         = self.packages.${system}.fleetman;
            fleetman        = mkFleetPkg { pname = "fleetman";        subPackage = "master"; };
            fleetman-server = mkFleetPkg { pname = "fleetman-server"; subPackage = "server"; };
          };

          devShells.default = pkgs.mkShell {
            buildInputs = with pkgs; [ go gopls gotools ];
          };
        }
      );

      # System-agnostic NixOS modules
      nixosModules = {
        fleetman-server = import ./nix/modules/fleetman-server.nix;
      };
    in
    perSystem // { inherit nixosModules; };
}
