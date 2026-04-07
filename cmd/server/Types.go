package main

import (
	"html/template"

	"github.com/gorilla/sessions"
)

type Env struct {
	Templates *template.Template
	Store     *sessions.CookieStore
}

// Define global variables
var templates *template.Template
var menuItems []MenuItem
var propertyItems map[string]any
var store *sessions.CookieStore

var adminLinks = []MenuItem{
	{Title: "Users", Href: "/t/_user"},
	{Title: "Groups", Href: "/t/_group"},
	{Title: "Group Memberships", Href: "/t/_group_membership"},
	{Title: "Roles", Href: "/t/_role"},
	{Title: "Role Assignments", Href: "/t/_group_role"},
	{Title: "User Preferences", Href: "/t/_user_preference"},
	{Title: "System Properties", Href: "/t/_property"},
	{Title: "Access Control", Href: "/admin/access"},
	{Title: "System Log", Href: "/admin/audit"},
	{Title: "Request Metrics", Href: "/admin/monitoring"},
	{Title: "Run Script", Href: "/admin/run-script"},
	{Title: "Application Editor", Href: "/admin/app-editor"},
}

// MenuItem represents the data for each menu item.
type MenuItem struct {
	Title string `json:"title"`
	Href  string `json:"href"`
	Order int    `json:"order"`
}

// User represents the currently logged in user.
type User struct {
	ID    string
	Name  string
	Email string
	Role  string
}

// PropertyItem represents a property item.
type PropertyItem struct {
	Name  string
	Value string
}

type pageData struct {
	Name       string
	Slug       string
	Content    string
	EditorMode string
	Status     string
}
