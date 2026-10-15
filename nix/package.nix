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
    pname = "file-station-frontend";
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
  pname = "file-station";
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
  subPackages = [ "cmd/file-station" ];
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];
  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    mkdir -p "$out/share/file-station"
    cp -r ${frontend}/. "$out/share/file-station/"
    wrapProgram "$out/bin/file-station" \
      --add-flags "-static $out/share/file-station"
  '';

  passthru = { inherit frontend; };
  meta = {
    description = "File Station for single-administrator shared-directory management";
    homepage = "https://github.com/CYL96/NixOS-File-Station";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "file-station";
  };
}
