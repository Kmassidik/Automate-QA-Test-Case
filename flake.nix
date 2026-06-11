{
  description = "Automate QA Test Case Generator — dev shell (qa-core + qa-ai)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          name = "qa-testcase-dev";

          # Tooling only — nothing installed globally (mirrors Leksa conventions).
          # Ollama runs NATIVELY on the host for Metal GPU access; it is intentionally
          # NOT provided here.
          packages = with pkgs; [
            go_1_22
            gopls
            gofumpt
            golangci-lint
            air
            gnumake
            jq
            httpie
            curl
          ];

          shellHook = ''
            echo "qa-testcase dev shell"
            echo "  go        $(go version | awk '{print $3}')"
            echo ""
            echo "  services/qa-core  — web UI, queue, SSE, exports, access gate"
            echo "  services/qa-ai    — stateless prompt -> Ollama -> validated JSON"
            echo ""
            echo "  Ollama runs NATIVELY on host (not in this shell, not in Docker):"
            echo "    ollama serve && ollama pull qwen2.5:7b"
            echo ""
            echo "  Run a service (hot reload):"
            echo "    (cd services/qa-ai   && set -a; source .env; set +a; air)"
            echo "    (cd services/qa-core && set -a; source .env; set +a; air)"
          '';
        };
      });
}
