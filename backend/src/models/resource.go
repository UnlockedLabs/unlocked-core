package models

import (
	"context"
	"math"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

type PaginatedResource[T any] struct {
	Message string         `json:"message"`
	Data    []T            `json:"data"`
	Meta    PaginationMeta `json:"meta,omitempty"`
}

type Resource[T any] struct {
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func DefaultResource[T any](data T) Resource[T] {
	return Resource[T]{
		Message: "resource fetched successfully",
		Data:    data,
	}
}

type PaginationMeta struct {
	CurrentPage int   `json:"current_page"`
	LastPage    int   `json:"last_page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
}

func NewPaginationInfo(currentPage, perPage int, total int64) PaginationMeta {
	lastPage := int(total) / perPage
	if int(total)%perPage > 0 {
		lastPage++
	}
	return PaginationMeta{
		CurrentPage: currentPage,
		LastPage:    lastPage,
		PerPage:     perPage,
		Total:       total,
	}
}

func UpdateStruct(dst, src any) {
	srcVal := reflect.ValueOf(src).Elem()
	dstVal := reflect.ValueOf(dst).Elem()

	if srcVal.Kind() == reflect.Pointer {
		srcVal = srcVal.Elem()
	}

	if dstVal.Kind() == reflect.Pointer {
		dstVal = dstVal.Elem()
	}

	for i := 0; i < srcVal.NumField(); i++ {
		srcField := srcVal.Field(i)
		dstField := dstVal.Field(i)

		if srcField.Kind() == reflect.Bool { //always set booleans
			dstField.Set(srcField)
			continue
		}

		if !reflect.DeepEqual(srcField.Interface(), reflect.Zero(srcField.Type()).Interface()) {
			dstField.Set(srcField)
		}
	}
}

func StringPtr(s string) *string {
	return &s
}

// NilIfBlank returns nil for empty or whitespace-only strings so they are stored as NULL.
func NilIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func UintPtr(u uint) *uint {
	return &u
}

func FormatNullableString(val *string) string {
	if val == nil {
		return ""
	}
	return *val
}

func DerefUint(ptr *uint) uint {
	if ptr == nil {
		return 0
	}
	return *ptr
}

type QueryContext struct {
	Params             url.Values
	Page               int
	PerPage            int
	FacilityID         uint
	UserID             uint
	OrderBy            string
	Order              string
	IsAdmin            bool
	CanSwitchFacility  bool
	Search             string
	Tags               []string
	Total              int64
	All                bool
	Ctx                context.Context
	Timezone           string
	IncludeDeactivated bool
	// FeatureAccess is the caller's effective (per-facility) feature set, for
	// queries that return a mix of content types and must drop the ones whose
	// sub-feature is disabled — a route-level gate can't express that.
	FeatureAccess []FeatureAccess
}

func (q *QueryContext) HasFeature(feature FeatureAccess) bool {
	return slices.Contains(q.FeatureAccess, feature)
}

func (q *QueryContext) MaybeID(key string) *int {
	var idptr *int
	id, err := strconv.Atoi(q.Params.Get(key))
	if err == nil {
		idptr = &id
	}
	return idptr
}

func (q QueryContext) IntoMeta() PaginationMeta {
	return NewPaginationInfo(q.Page, q.PerPage, q.Total)
}

func (q QueryContext) CalcOffset() int {
	return int(math.Abs(float64((q.Page - 1) * q.PerPage)))
}

// fallback column is used if there is no order by in the query string
func (q QueryContext) OrderClause(fallback string) string {
	val := q.OrderBy + " " + q.Order
	if val == " " || q.OrderBy == "" {
		return fallback
	}
	return val
}

func (q QueryContext) SearchQuery() string {
	if q.Search == "" {
		return ""
	}
	return "%" + q.Search + "%"
}
