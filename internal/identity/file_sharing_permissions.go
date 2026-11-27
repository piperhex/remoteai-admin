package identity

func addFileSharingPermissions(cat *permissionCatalog) {
	cat.Permissions = append(cat.Permissions,
		permission{Code: "admin.file-sharing.read", Name: "fileSharing.permissionRead",
			Group: "fileSharing.permissionGroup", Description: "fileSharing.permissionReadDescription"},
		permission{Code: "admin.file-sharing.manage", Name: "fileSharing.permissionManage",
			Group: "fileSharing.permissionGroup", Description: "fileSharing.permissionManageDescription"},
	)
	cat.Dependencies["admin.file-sharing.manage"] = []string{"admin.file-sharing.read"}
}
