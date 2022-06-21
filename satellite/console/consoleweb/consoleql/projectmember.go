// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package consoleql

import (
	"time"

	"github.com/graphql-go/graphql"

	"storj.io/storj/satellite/console"
)

const (
	// ProjectMemberType is a graphql type name for project member.
	ProjectMemberType = "projectMember"
	// FieldJoinedAt is a field name for joined at timestamp.
	FieldJoinedAt = "joinedAt"
)

// graphqlProjectMember creates projectMember type.
func graphqlProjectMember(service *console.Service, types *TypeCreator) *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: ProjectMemberType,
		Fields: graphql.Fields{
			UserType: &graphql.Field{
				Type: types.user,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					member, _ := p.Source.(projectMember)
					// company sub query expects pointer
					return member.User, nil
				},
			},
			FieldJoinedAt: &graphql.Field{
				Type: graphql.DateTime,
			},
		},
	})
}

func graphqlProjectMembersCursor() *graphql.InputObject {
	return graphql.NewInputObject(graphql.InputObjectConfig{
		Name: ProjectMembersCursorInputType,
		Fields: graphql.InputObjectConfigFieldMap{
			SearchArg: &graphql.InputObjectFieldConfig{
				Type: graphql.NewNonNull(graphql.String),
			},
			LimitArg: &graphql.InputObjectFieldConfig{
				Type: graphql.NewNonNull(graphql.Int),
			},
			PageArg: &graphql.InputObjectFieldConfig{
				Type: graphql.NewNonNull(graphql.Int),
			},
			OrderArg: &graphql.InputObjectFieldConfig{
				Type: graphql.NewNonNull(graphql.Int),
			},
			OrderDirectionArg: &graphql.InputObjectFieldConfig{
				Type: graphql.NewNonNull(graphql.Int),
			},
		},
	})
}

func graphqlProjectMembersPage(types *TypeCreator) *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name: ProjectMembersPageType,
		Fields: graphql.Fields{
			FieldProjectMembers: &graphql.Field{
				Type: graphql.NewList(types.projectMember),
			},
			SearchArg: &graphql.Field{
				Type: graphql.String,
			},
			LimitArg: &graphql.Field{
				Type: graphql.Int,
			},
			OrderArg: &graphql.Field{
				Type: graphql.Int,
			},
			OrderDirectionArg: &graphql.Field{
				Type: graphql.Int,
			},
			OffsetArg: &graphql.Field{
				Type: graphql.Int,
			},
			FieldPageCount: &graphql.Field{
				Type: graphql.Int,
			},
			FieldCurrentPage: &graphql.Field{
				Type: graphql.Int,
			},
			FieldTotalCount: &graphql.Field{
				Type: graphql.Int,
			},
		},
	})
}

// projectMember encapsulates User and joinedAt.
type projectMember struct {
	User     *console.User
	JoinedAt time.Time
}

type projectMembersPage struct {
	ProjectMembers []projectMember

	Search         string
	Limit          uint
	Order          int
	OrderDirection int
	Offset         uint64

	PageCount   uint
	CurrentPage uint
	TotalCount  uint64
}
