package identity

func addUpdateSharingPermissions(cat *permissionCatalog) {
	cat.Permissions = append(cat.Permissions,
		permission{Code: "admin.update-sharing.read", Name: "View download settings", Group: "Content",
			Description: "View package sharing and download optimization settings."},
		permission{Code: "admin.update-sharing.manage", Name: "Manage download settings", Group: "Content",
			Description: "Manage package sharing and download optimization settings."},
	)
	cat.Dependencies["admin.update-sharing.manage"] = []string{"admin.update-sharing.read"}
}
