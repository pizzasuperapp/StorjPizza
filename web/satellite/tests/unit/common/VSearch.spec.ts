// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

import SearchComponent from '@/components/common/VSearch.vue';

import { mount, shallowMount } from '@vue/test-utils';

describe('SearchComponent.vue', () => {
    it('renders correctly', () => {
        const wrapper = shallowMount<SearchComponent>(SearchComponent);

        expect(wrapper).toMatchSnapshot();
    });

    it('renders correctly with default props', () => {
        const wrapper = mount<SearchComponent>(SearchComponent);

        expect(wrapper.vm.$props.placeholder).toMatch('');
    });

    it('functions onMouseEnter/onMouseLeave work correctly', () => {
        const wrapper = mount<SearchComponent>(SearchComponent);

        wrapper.vm.onMouseEnter();

        expect(wrapper.vm.style.width).toMatch('540px');

        wrapper.vm.onMouseLeave();

        expect(wrapper.vm.searchString).toMatch('');
        expect(wrapper.vm.style.width).toMatch('56px');
    });

    it('function clearSearch works correctly', () => {
        const processSearchQuerySpy = jest.fn();

        const wrapper = mount<SearchComponent>(SearchComponent);

        wrapper.vm.processSearchQuery = processSearchQuerySpy;
        wrapper.vm.clearSearch();

        expect(processSearchQuerySpy).toHaveBeenCalledTimes(1);
        expect(wrapper.vm.$data.inputWidth).toMatch('56px');
    });
});
