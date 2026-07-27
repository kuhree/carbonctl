{
  buildGoModule,
  lib,
}:
buildGoModule {
  pname = "carbonctl";
  version = "0.1.0";

  src = ./.;
  vendorHash = "sha256-0Qxw+MUYVgzgWB8vi3HBYtVXSq/btfh4ZfV/m1chNrA=";

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
