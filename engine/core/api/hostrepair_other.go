//go:build !darwin

package api

import "NekkoVPN/core/netdoctor"

// On Windows the settings the doctor repairs are the signed-in user's (HKCU),
// so it repairs them right here, in the GUI process.

func repairHost() (netdoctor.RepairResult, error) { return netdoctor.Repair() }

func sanitizeHostProxy() (netdoctor.Action, error) { return netdoctor.SanitizeProxy() }
