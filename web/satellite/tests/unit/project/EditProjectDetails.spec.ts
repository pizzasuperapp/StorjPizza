// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

import Vuex from 'vuex';

import EditProjectDetails from '@/components/project/EditProjectDetails.vue';

import { makeProjectsModule, PROJECTS_MUTATIONS } from '@/store/modules/projects';
import { Project, ProjectLimits } from '@/types/projects';
import { NotificatorPlugin } from '@/utils/plugins/notificator';
import { createLocalVue, mount } from '@vue/test-utils';

import { ProjectsApiMock } from '../mock/api/projects';
import {makeNotificationsModule} from "@/store/modules/notifications";

const localVue = createLocalVue();
localVue.use(Vuex);

const projectLimits = new ProjectLimits(1000, 100, 1000, 100);
const projectsApi = new ProjectsApiMock();
projectsApi.setMockLimits(projectLimits);
const projectsModule = makeProjectsModule(projectsApi);
const notificationsModule = makeNotificationsModule();

const store = new Vuex.Store({
    modules: {
        projectsModule,
        notificationsModule,
        usersModule: {
            state: {
                user: { paidTier: false },
            }
        }
    }});

localVue.use(new NotificatorPlugin(store));

const project = new Project('id', 'test', 'test', 'test', 'ownedId', false);

describe('EditProjectDetails.vue', () => {
    it('renders correctly', (): void => {
        store.commit(PROJECTS_MUTATIONS.ADD, project);
        store.commit(PROJECTS_MUTATIONS.SELECT_PROJECT, project.id);
        store.commit(PROJECTS_MUTATIONS.SET_LIMITS, projectLimits);

        const wrapper = mount<EditProjectDetails>(EditProjectDetails, {
            store,
            localVue,
        });

        expect(wrapper).toMatchSnapshot();
    });

    it('editing name works correctly', async (): Promise<void> => {
        const wrapper = mount<EditProjectDetails>(EditProjectDetails, {
            store,
            localVue,
        });

        await wrapper.vm.toggleNameEditing();

        expect(wrapper).toMatchSnapshot();

        const newName = 'new name';

        wrapper.vm.$data.nameValue = newName;
        await wrapper.vm.onSaveNameButtonClick();

        expect(wrapper).toMatchSnapshot();
        await expect(store.getters.selectedProject.name).toMatch(newName);
    });

    it('editing description works correctly', async (): Promise<void> => {
        const wrapper = mount<EditProjectDetails>(EditProjectDetails, {
            store,
            localVue,
        });

        await wrapper.vm.toggleDescriptionEditing();

        expect(wrapper).toMatchSnapshot();

        const newDescription = 'new description';

        wrapper.vm.$data.descriptionValue = newDescription;
        await wrapper.vm.onSaveDescriptionButtonClick();

        expect(wrapper).toMatchSnapshot();
        await expect(store.getters.selectedProject.description).toMatch(newDescription);
    });
});
