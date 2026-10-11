{
  description = "An MCP server for an OpenCentauri printer";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: {
        default = pkgs.callPackage ./package.nix { };
      });

      nixosModules.default = import ./module.nix self;

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
          ];
        };
      });

      checks = forAll (pkgs: {
        # The build runs `go test ./...`.
        package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        module = pkgs.testers.runNixOSTest {
          name = "carbonctl";
          nodes.machine = {
            imports = [ self.nixosModules.default ];
            services.carbonctl = {
              enable = true;
              printer = "127.0.0.2";
            };
          };
          testScript = ''
            machine.wait_for_unit("carbonctl.service")
            machine.wait_for_open_port(9086)
          '';
        };
      });

      formatter = forAll (pkgs: pkgs.nixfmt);
    };
}
