package platform

import (
	"fmt"
	"strings"
)

// EmployeeID is exactly six ASCII digits (REG-002).
type EmployeeID string

func ParseEmployeeID(s string) (EmployeeID, error) {
	if len(s) != 6 {
		return "", fmt.Errorf("employee id must be exactly 6 digits")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", fmt.Errorf("employee id must contain digits only")
		}
	}
	return EmployeeID(s), nil
}

func (e EmployeeID) String() string { return string(e) }

// Department is a fixed company code (REG-004).
type Department string

const (
	DepartmentTSID Department = "TSID"
	DepartmentBCID Department = "BCID"
	DepartmentDEID Department = "DEID"
	DepartmentNTAD Department = "NTAD"
)

func ParseDepartment(s string) (Department, error) {
	d := Department(s)
	switch d {
	case DepartmentTSID, DepartmentBCID, DepartmentDEID, DepartmentNTAD:
		return d, nil
	}
	return "", fmt.Errorf("unknown department %q", s)
}

func (d Department) String() string { return string(d) }

// Track is the registration type. The code is stable; display names are
// translated by the frontend (I18N-005).
type Track string

const (
	TrackSmartInnovation        Track = "smart_innovation"
	TrackSustainableDevelopment Track = "sustainable_development"
)

func ParseTrack(s string) (Track, error) {
	t := Track(strings.TrimSpace(s))
	switch t {
	case TrackSmartInnovation, TrackSustainableDevelopment:
		return t, nil
	}
	return "", fmt.Errorf("unknown track %q", s)
}

func (t Track) String() string { return string(t) }

// AwardCategory is the award a team competes for.
type AwardCategory string

const (
	CategoryEngineering       AwardCategory = "engineering"
	CategoryAdministration    AwardCategory = "administration"
	CategoryManufacturingProc AwardCategory = "manufacturing_process"
	CategoryTransportation    AwardCategory = "transportation"
	CategoryPublicSafety      AwardCategory = "public_safety"
)

func ParseAwardCategory(s string) (AwardCategory, error) {
	c := AwardCategory(strings.TrimSpace(s))
	switch c {
	case CategoryEngineering, CategoryAdministration, CategoryManufacturingProc,
		CategoryTransportation, CategoryPublicSafety:
		return c, nil
	}
	return "", fmt.Errorf("unknown award category %q", s)
}

func (c AwardCategory) String() string { return string(c) }
