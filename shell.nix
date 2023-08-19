{ pkgs ? import <nixpkgs> {} }:

let
	go = pkgs.callPackage ./nix/go.nix {};
in

pkgs.mkShell {
	buildInputs = with pkgs; [
		go
	];
}
