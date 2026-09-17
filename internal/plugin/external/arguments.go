package external

import (
	"errors"
	"strconv"
	"strings"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
)

const ManagementUsage = "plugin list/status/install DIR/update ID DIR/uninstall ID [--purge]/enable/disable/restart/grant/revoke ID CAPABILITY [all|context|workspace:IDs|pane:IDs]; plugin run ID COMMAND [ARGS...]"

func ParseManagement(args []string) (v1.ManageRequest, error) {
	var r v1.ManageRequest
	if len(args) == 0 {
		return r, errors.New(ManagementUsage)
	}
	r.Action = args[0]
	a := args[1:]
	switch r.Action {
	case "list":
		if len(a) != 0 {
			return r, errors.New(ManagementUsage)
		}
	case "status":
		if len(a) > 1 {
			return r, errors.New(ManagementUsage)
		}
		if len(a) == 1 {
			r.ID = a[0]
		}
	case "install":
		if len(a) != 1 {
			return r, errors.New(ManagementUsage)
		}
		r.Directory = a[0]
	case "update":
		if len(a) != 2 {
			return r, errors.New(ManagementUsage)
		}
		r.ID = a[0]
		r.Directory = a[1]
	case "uninstall":
		if len(a) == 2 && a[1] == "--purge" {
			r.Purge = true
			a = a[:1]
		}
		fallthrough
	case "enable", "disable", "restart":
		if len(a) != 1 {
			return r, errors.New(ManagementUsage)
		}
		r.ID = a[0]
	case "run":
		if len(a) < 2 {
			return r, errors.New(ManagementUsage)
		}
		r.ID = a[0]
		r.Command = a[1]
		r.Args = a[2:]
	case "grant", "revoke":
		if len(a) < 2 || len(a) > 3 {
			return r, errors.New(ManagementUsage)
		}
		r.ID = a[0]
		g := v1.Grant{Capability: v1.Capability(a[1]), Scope: v1.Scope{Kind: "all"}}
		if len(a) == 3 {
			kind, ids, hasIDs := strings.Cut(a[2], ":")
			g.Scope.Kind = kind
			if hasIDs {
				for _, part := range strings.Split(ids, ",") {
					id, err := strconv.ParseUint(part, 10, 64)
					if err != nil {
						return r, err
					}
					g.Scope.IDs = append(g.Scope.IDs, id)
				}
			}
		}
		if err := ValidateGrant(g); err != nil {
			return r, err
		}
		r.Grant = &g
	default:
		return r, errors.New(ManagementUsage)
	}
	return r, nil
}
