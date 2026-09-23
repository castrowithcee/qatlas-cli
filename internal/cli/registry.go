package cli

import (
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/provider/lexware"
	"github.com/castrowithcee/qatlas-cli/internal/provider/nextcloud"
	"github.com/castrowithcee/qatlas-cli/internal/provider/seatable"
	"github.com/castrowithcee/qatlas-cli/internal/provider/telegram"
	"github.com/castrowithcee/qatlas-cli/internal/provider/twentycrm"
)

// defaultRegistry wires every provider implementation this build ships. Registration is static, so a
// failure here is a programming error rather than a runtime condition; TestDefaultRegistry proves it. Every
// provider wired here also runs through the shared conformance checks of TestProviderConformance.
func defaultRegistry() *capability.Registry {
	reg := capability.NewRegistry()
	if err := bookstack.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := telegram.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := lexware.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := twentycrm.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := seatable.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := nextcloud.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	if err := github.Register(reg); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	// Tool profiles name registered tools, so they are checked once every provider has registered its own.
	if err := reg.ValidateProfiles(); err != nil {
		panic("provider registration is static and must not fail: " + err.Error())
	}
	return reg
}
