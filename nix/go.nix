{ lib, fetchFromGitHub, fetchurl }:

let
	pkgs = import (fetchFromGitHub {
		owner = "NixOS";
		repo = "nixpkgs";
		rev = "1bf8d5f5d53f2f5fdd4debe04cae45b4cb32641e";
		sha256 = "sha256-wnvVAKRaLy92U/9fSzmnqYwLqCLKJK5VE4s+jPEhloI=";
	}) {};
in

# Pending PR:
# https://github.com/NixOS/nixpkgs/pull/248027

pkgs.go_1_21.overrideAttrs (old: rec {
	version = "1.21.0";
	src = fetchurl {
		url = "https://go.dev/dl/go${version}.src.tar.gz";
		hash = "sha256-gY1G7ehWgt1VGtN47zek0kcAbxLsWbW3VWAdLOEUNpo=";
	};
})
