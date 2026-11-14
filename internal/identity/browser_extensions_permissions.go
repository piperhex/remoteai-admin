package identity

func addBrowserExtensionsPermissions(cat *permissionCatalog) {
	cat.Permissions = append(cat.Permissions,
		permission{Code: "admin.browser-extensions.read", Name: "browserExtensions.permissionRead", Group: "browserExtensions.permissionGroup",
			Description: "browserExtensions.permissionReadDescription"},
		permission{Code: "admin.browser-extensions.manage", Name: "browserExtensions.permissionManage", Group: "browserExtensions.permissionGroup",
			Description: "browserExtensions.permissionManageDescription"},
	)
	cat.Dependencies["admin.browser-extensions.manage"] = []string{"admin.browser-extensions.read"}
}
