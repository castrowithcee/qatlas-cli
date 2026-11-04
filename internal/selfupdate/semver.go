package selfupdate

import releaseverify "github.com/castrowithcee/qatlas-connect/internal/release"

type semver = releaseverify.Version

func parseSemver(value string) (semver, error) { return releaseverify.ParseVersion(value) }
