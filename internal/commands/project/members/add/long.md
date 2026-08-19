Add a member to the project with the specified role.

Roles:

- guest (10): Can view the project.
- reporter (20): Can view and create issues.
- developer (30): Can push to non-protected branches.
- maintainer (40): Can manage the project.
- owner (50): Full access to the project.

For custom roles, use `--role-id` with the ID of a custom role defined in the project or group.
If the custom role does not exist, an error is returned.
