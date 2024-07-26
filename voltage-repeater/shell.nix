{
  pkgs ? import <nixpkgs> {} 
}: 

pkgs.mkShell {
  nativeBuildInputs = [
    pkgs.go
    pkgs.gopls
  ];
  
  buildInputs = [
  ];
}
