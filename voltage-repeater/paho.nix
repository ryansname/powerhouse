{ lib
, buildPythonPackage
, fetchFromGitHub
, hatchling
, setuptools
, wheel
}:

buildPythonPackage rec {
  pname = "paho-mqtt";
  version = "2.1.0";
  pyproject = true;

  src = fetchFromGitHub {
    owner = "eclipse";
    repo = "paho.mqtt.python";
    rev = "v${version}";
    hash = "sha256-VMq+WTW+njK34QUUTE6fR2j2OmHxVzR0wrC92zYb1rY=";
  };

  build-system = [
    hatchling
  ];

  doCheck = false;
  pythonImportsCheck = [
    "paho.mqtt"
  ];
}
