{
  lib,
  buildGoModule,
  buildNpmPackage,
  importNpmLock,
  go_1_26,
  nodejs_24,
  makeWrapper,
}:
let
  version = "1.0.0";
  frontend = buildNpmPackage {
    pname = "nixos-webui-frontend";
    inherit version;
    src = lib.cleanSource ../web;
    nodejs = nodejs_24;

    # Each tarball is pinned by the integrity field in the npm lockfile.
    npmDeps = importNpmLock { npmRoot = ../web; };
    npmConfigHook = importNpmLock.npmConfigHook;
    npmBuildScript = "build";

    installPhase = ''
      runHook preInstall
      mkdir -p "$out"
      cp -r dist/. "$out/"
      runHook postInstall
    '';
  };
in
(buildGoModule.override { go = go_1_26; }) {
  pname = "nixos-webui";
  inherit version;
  src = lib.cleanSourceWith {
    src = lib.cleanSource ../.;
    filter = path: _type:
      let
        relative = lib.removePrefix (toString ../. + "/") (toString path);
        topLevel = builtins.head (lib.splitString "/" relative);
      in !builtins.elem topLevel [ "web" "node_modules" "result" ".direnv" ];
  };
  vendorHash = import ./vendor-hash.nix;
  subPackages = [ "cmd/webui" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    mkdir -p "$out/share/nixos-webui"
    cp -r ${frontend}/. "$out/share/nixos-webui/"
    mv "$out/bin/webui" "$out/bin/nixos-webui"
    wrapProgram "$out/bin/nixos-webui" \
      --add-flags "-static $out/share/nixos-webui"
  '';

  passthru = { inherit frontend; };
  meta = {
    description = "Single-administrator File Station for NixOS shared directories";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "nixos-webui";
  };
}
