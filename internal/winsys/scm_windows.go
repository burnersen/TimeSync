//go:build windows

package winsys

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// connectReadOnly opens the service manager with the rights every user has,
// so status queries work without administrator rights.
func connectReadOnly() (*mgr.Mgr, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	return &mgr.Mgr{Handle: h}, nil
}

func openServiceReadOnly(m *mgr.Mgr, name string) (*mgr.Service, error) {
	h, err := windows.OpenService(m.Handle, windows.StringToUTF16Ptr(name), windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: name, Handle: h}, nil
}

// ServiceInfo is what any user may learn about a service.
type ServiceInfo struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	State     string `json:"state"`
	StartType string `json:"startType"`
	BinPath   string `json:"binPath"`
	Account   string `json:"account"`
}

// QueryService reports on a service without needing administrator rights.
func QueryService(name string) ServiceInfo {
	var info ServiceInfo
	m, err := connectReadOnly()
	if err != nil {
		return info
	}
	defer m.Disconnect()
	s, err := openServiceReadOnly(m, name)
	if err != nil {
		return info
	}
	defer s.Close()
	info.Installed = true
	if cfg, err := s.Config(); err == nil {
		info.StartType = startTypeName(cfg.StartType, cfg.DelayedAutoStart)
		info.BinPath = cfg.BinaryPathName
		info.Account = cfg.ServiceStartName
	}
	if q, err := s.Query(); err == nil {
		info.State = stateName(uint32(q.State))
		info.Running = q.State == windows.SERVICE_RUNNING
	}
	return info
}

func stateName(s uint32) string {
	switch s {
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "starting"
	case windows.SERVICE_STOP_PENDING:
		return "stopping"
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_PAUSED:
		return "paused"
	}
	return "unknown"
}
