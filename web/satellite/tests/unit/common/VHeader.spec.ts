// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

import HeaderComponent from '@/components/common/VHeader.vue';

import { mount, shallowMount } from '@vue/test-utils';

describe('HeaderComponent.vue', () => {
    it('renders correctly', () => {
        const wrapper = shallowMount<HeaderComponent>(HeaderComponent);

        expect(wrapper).toMatchSnapshot();
    });

    it('renders correctly with default props', () => {
        const wrapper = mount<HeaderComponent>(HeaderComponent);
        expect(wrapper.vm.$props.placeholder).toMatch('');
    });

    it('function clearSearch works correctly', () => {
        const search = jest.fn();

        const wrapper = mount<HeaderComponent>(HeaderComponent, {
            propsData: {
                search: search,
            }
        });
        wrapper.vm.clearSearch();
        expect(search).toHaveBeenCalledTimes(1);
    });
});
