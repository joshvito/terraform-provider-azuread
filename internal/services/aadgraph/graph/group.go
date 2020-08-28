package graph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/graphrbac/1.6/graphrbac"
)

type GroupMemberId struct {
	ObjectSubResourceId
	GroupId  string
	MemberId string
}

func GroupMemberIdFrom(groupId, memberId string) GroupMemberId {
	return GroupMemberId{
		ObjectSubResourceId: ObjectSubResourceIdFrom(groupId, "member", memberId),
		GroupId:             groupId,
		MemberId:            memberId,
	}
}

func ParseGroupMemberId(idString string) (*GroupMemberId, error) {
	id, err := ParseObjectSubResourceId(idString, "member")
	if err != nil {
		return nil, fmt.Errorf("unable to parse Member ID: %v", err)
	}

	return &GroupMemberId{
		ObjectSubResourceId: *id,
		GroupId:             id.objectId,
		MemberId:            id.subId,
	}, nil
}

func GroupGetByDisplayName(ctx context.Context, client *graphrbac.GroupsClient, displayName string) (*graphrbac.ADGroup, error) {
	filter := fmt.Sprintf("displayName eq '%s'", displayName)

	resp, err := client.ListComplete(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("listing Groups for filter %q: %+v", filter, err)
	}

	values := resp.Response().Value
	if values == nil {
		return nil, fmt.Errorf("nil values for Groups matching %q", filter)
	}
	if len(*values) == 0 {
		return nil, fmt.Errorf("found no Groups matching %q", filter)
	}
	if len(*values) > 2 {
		return nil, fmt.Errorf("found multiple Groups matching %q", filter)
	}

	group := (*values)[0]
	if group.DisplayName == nil {
		return nil, fmt.Errorf("nil DisplayName for Group matching %q", filter)
	}
	if !strings.EqualFold(*group.DisplayName, displayName) {
		return nil, fmt.Errorf("displayname for Group matching %q does not match (%q!=%q)", filter, *group.DisplayName, displayName)
	}

	return &group, nil
}

func DirectoryObjectListToIDs(ctx context.Context, objects graphrbac.DirectoryObjectListResultIterator) ([]string, error) {
	errBase := "during pagination of directory objects"
	ids := make([]string, 0)
	for objects.NotDone() {
		v := objects.Value()

		// possible members are users, groups or service principals
		// we try to 'cast' each result as the corresponding type and diff
		// if we found the object we're looking for
		user, _ := v.AsUser()
		if user != nil {
			if user.ObjectID == nil {
				return nil, fmt.Errorf("user with null object ID encountered %s", errBase)
			}
			ids = append(ids, *user.ObjectID)
		}

		group, _ := v.AsADGroup()
		if group != nil {
			if group.ObjectID == nil {
				return nil, fmt.Errorf("group with null object ID encountered %s", errBase)
			}
			ids = append(ids, *group.ObjectID)
		}

		servicePrincipal, _ := v.AsServicePrincipal()
		if servicePrincipal != nil {
			if servicePrincipal.ObjectID == nil {
				return nil, fmt.Errorf("service principal with null object ID encountered %s", errBase)
			}
			ids = append(ids, *servicePrincipal.ObjectID)
		}

		if err := objects.NextWithContext(ctx); err != nil {
			return nil, fmt.Errorf("%s: %+v", errBase, err)
		}
	}

	return ids, nil
}

func GroupAllMembers(ctx context.Context, client *graphrbac.GroupsClient, groupId string) ([]string, error) {
	members, err := client.GetGroupMembersComplete(ctx, groupId)

	if err != nil {
		return nil, fmt.Errorf("listing existing group members from Group with ID %q: %+v", groupId, err)
	}

	existingMembers, err := DirectoryObjectListToIDs(ctx, members)
	if err != nil {
		return nil, fmt.Errorf("getting object IDs of group members for Group with ID %q: %+v", groupId, err)
	}

	return existingMembers, nil
}

func GroupAddMember(ctx context.Context, client *graphrbac.GroupsClient, groupId string, member string) error {
	memberGraphURL := fmt.Sprintf("%s/%s/directoryObjects/%s", strings.TrimRight(client.BaseURI, "/"), client.TenantID, member)

	properties := graphrbac.GroupAddMemberParameters{
		URL: &memberGraphURL,
	}

	var err error
	attempts := 10
	for i := 0; i <= attempts; i++ {
		if _, err = client.AddMember(ctx, groupId, properties); err == nil {
			break
		}
		if i == attempts {
			return fmt.Errorf("adding group member %q to Group with ID %q: %+v", member, groupId, err)
		}
		time.Sleep(time.Second * 2)
	}

	if _, err := WaitForListAdd(member, func() ([]string, error) {
		return GroupAllMembers(ctx, client, groupId)
	}); err != nil {
		return fmt.Errorf("waiting for group membership: %+v", err)
	}

	return nil
}

func GroupAddMembers(ctx context.Context, client *graphrbac.GroupsClient, groupId string, members []string) error {
	for _, memberUuid := range members {
		err := GroupAddMember(ctx, client, groupId, memberUuid)

		if err != nil {
			return fmt.Errorf("while adding members to Group with ID %q: %+v", groupId, err)
		}
	}

	return nil
}

func GroupAllOwners(ctx context.Context, client *graphrbac.GroupsClient, groupId string) ([]string, error) {
	owners, err := client.ListOwnersComplete(ctx, groupId)

	if err != nil {
		return nil, fmt.Errorf("listing existing group owners from Group with ID %q: %+v", groupId, err)
	}

	existingMembers, err := DirectoryObjectListToIDs(ctx, owners)
	if err != nil {
		return nil, fmt.Errorf("getting objects IDs of group owners for Group with ID %q: %+v", groupId, err)
	}

	return existingMembers, nil
}

func GroupAddOwner(ctx context.Context, client *graphrbac.GroupsClient, groupId string, owner string) error {
	ownerGraphURL := fmt.Sprintf("%s/%s/directoryObjects/%s", strings.TrimRight(client.BaseURI, "/"), client.TenantID, owner)

	properties := graphrbac.AddOwnerParameters{
		URL: &ownerGraphURL,
	}

	if _, err := client.AddOwner(ctx, groupId, properties); err != nil {
		return fmt.Errorf("adding group owner %q to Group with ID %q: %+v", owner, groupId, err)
	}

	return nil
}

func GroupAddOwners(ctx context.Context, client *graphrbac.GroupsClient, groupId string, owner []string) error {
	for _, ownerUuid := range owner {
		err := GroupAddOwner(ctx, client, groupId, ownerUuid)

		if err != nil {
			return fmt.Errorf("while adding owners to Group with ID %q: %+v", groupId, err)
		}
	}

	return nil
}

func GroupFindByName(ctx context.Context, client *graphrbac.GroupsClient, name string) (*graphrbac.ADGroup, error) {
	nameFilter := fmt.Sprintf("displayName eq '%s'", name)
	resp, err := client.List(ctx, nameFilter)

	if err != nil {
		return nil, fmt.Errorf("unable to list Groups with filter %q: %+v", nameFilter, err)
	}

	for _, group := range resp.Values() {
		if group.DisplayName != nil && *group.DisplayName == name {
			return &group, nil
		}
	}

	return nil, nil
}

func GroupCheckNameAvailability(ctx context.Context, client *graphrbac.GroupsClient, name string) error {
	existingGroup, err := GroupFindByName(ctx, client, name)
	if err != nil {
		return err
	}
	if existingGroup != nil {
		return fmt.Errorf("existing Group with name %q (ID: %q) was found and `prevent_duplicate_names` was specified", name, *existingGroup.ObjectID)
	}
	return nil
}
