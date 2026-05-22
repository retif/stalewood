{
  description = "stalewood — find and reap merged git worktrees";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      # `nix develop` — Go toolchain + LSP + linters + just
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls      # language server
            gotools    # goimports etc.
            go-tools   # staticcheck
            just
          ];
        };
      });

      # `nix build` / `nix run` — the tool itself
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "stalewood";
          version = nixpkgs.lib.fileContents ./VERSION;
          src = ./.;
          vendorHash = null; # stdlib only — no module dependencies
          meta = {
            description = "Find and reap merged git worktrees";
            mainProgram = "stalewood";
          };
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
