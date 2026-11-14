package identity

func addBrowserHomePermissions(cat *permissionCatalog) {
	cat.Permissions = append(cat.Permissions,
		permission{Code: "admin.browser-home.read", Name: "browserHome.permissionRead", Group: "browserHome.permissionGroup",
			Description: "browserHome.permissionReadDescription"},
		permission{Code: "admin.browser-home.manage", Name: "browserHome.permissionManage", Group: "browserHome.permissionGroup",
			Description: "browserHome.permissionManageDescription"},
	)
	cat.Dependencies["admin.browser-home.manage"] = []string{"admin.browser-home.read"}
}
