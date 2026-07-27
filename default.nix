{
  buildGoModule,
  lib,
}:
buildGoModule {
  pname = "carbonctl";
  version = "0.1.0";

  src = ./.;
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
