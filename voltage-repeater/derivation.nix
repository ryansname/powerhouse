{ lib, python3Packages }:
with python3Packages;
buildPythonApplication {
  pname = "voltage-repeater";
  version = "1.0";

  propagatedBuildInputs = [ 
    (python3Packages.callPackage ./paho.nix {})
    (python3Packages.callPackage ./victron-ble.nix {})
  ];

  src = ./.;
}
