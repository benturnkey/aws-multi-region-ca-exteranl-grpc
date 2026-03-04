{
  description = "aws-multi-region-ca-exteranl-grpc";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            golangci-lint
          ];
        };

        packages.default = pkgs.buildGoModule {
          pname = "aws-multi-region-ca-exteranl-grpc";
          version = "0.0.1";
          src = ./.;
          vendorHash = null;
          subPackages = [ "cmd/server" ];
        };
      });
}
