{ lib
, bleak
, click
, construct
, pycryptodome    
, buildPythonPackage
, fetchFromGitHub
, hatchling
, setuptools
, wheel
}:

buildPythonPackage rec {
  pname = "victron-ble";
  version = "0.6.0";
  pyproject = true;

  src = fetchFromGitHub {
    owner = "keshavdv";
    repo = "victron-ble";
    rev = "v${version}";
    hash = "sha256-iR49erKlzlFt7F75kZRlCPyipvf6lmEnypBKevxjOss=";
  };

  build-system = [
    # hatchling
    setuptools
    wheel
  ];

  doCheck = false;
  pythonImportsCheck = [
    "victron_ble"
    "victron_ble.devices"
  ];

  dependencies = [
    bleak
    click
    construct
    pycryptodome    
  ];
}

