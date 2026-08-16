{
  buildGoModule,
  lib,
}:
buildGoModule {
  pname = "carbonctl";
  version = "0.1.0";

  # Go sources and the module files only, so editing this package's README,
  # its NixOS module or its module tests does not rebuild the binary — and
  # does not move the closure of every host that installs it.
  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      (lib.fileset.fileFilter (file: file.hasExt "go") ./.)
    ];
  };
  vendorHash = "sha256-IJjfVgO/IU7/22RamaXhVjDzh4cEug3bO68O4mAwDUM=";

  subPackages = [ "cmd/carbonctl" ];

  ldflags = [
    "-s"
    "-w"
  ];

  meta = {
    description = "Local MCP bridge for an OpenCentauri printer";
    mainProgram = "carbonctl";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
  };
}
