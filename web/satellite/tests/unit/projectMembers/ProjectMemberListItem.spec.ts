// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

import Vuex from 'vuex';

import ProjectMemberListItem from '@/components/team/ProjectMemberListItem.vue';

import { ProjectsApiGql } from '@/api/projects';
import { makeProjectsModule, PROJECTS_MUTATIONS } from '@/store/modules/projects';
import { ProjectMember } from '@/types/projectMembers';
import { Project } from '@/types/projects';
import { createLocalVue, mount } from '@vue/test-utils';

const localVue = createLocalVue();

localVue.use(Vuex);

describe('', () => {
    const pApi = new ProjectsApiGql();
    const projectsModule = makeProjectsModule(pApi);
    const data = new Date(0);
    const member: ProjectMember = new ProjectMember('testFullName', 'testShortName', 'test@example.com', data, '1');
    const project: Project = new Project('testId', 'testName', 'testDescr', 'testDate', '1');

    const store = new Vuex.Store({modules: { projectsModule }});

    store.commit(PROJECTS_MUTATIONS.SET_PROJECTS, [project]);
    store.commit(PROJECTS_MUTATIONS.SELECT_PROJECT, 'testId');

    it('should render correctly', function () {
        const wrapper = mount(ProjectMemberListItem, {
            store,
            localVue,
            propsData: {
                itemData: member,
            },
        });

        expect(wrapper).toMatchSnapshot();
        expect(store.getters.selectedProject.ownerId).toBe(member.user.id);
        expect(wrapper.findAll('.user-container__base-info__name-area__owner-status').length).toBe(1);
    });

    it('should render correctly with item row highlighted', function () {
        member.isSelected = true;

        const wrapper = mount(ProjectMemberListItem, {
            store,
            localVue,
            propsData: {
                itemData: member,
            },
        });

        expect(wrapper).toMatchSnapshot();
    });
});
