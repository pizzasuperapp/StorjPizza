// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

import { SortDirection } from '@/types/common';

export type OnHeaderClickCallback = (sortBy: AccessGrantsOrderBy, sortDirection: SortDirection) => Promise<void>;

/**
 * AccessGrantsWorker provides access to the WASM module.
 */
export interface AccessGrantsWorkerFactory {
    // TODO: this should be converted to a proper interface.
    create(): Worker;
}

/**
 * Exposes all access grants-related functionality.
 */
export interface AccessGrantsApi {
    /**
     * Fetch accessGrants
     *
     * @returns AccessGrantsPage
     * @throws Error
     */
    get(projectId: string, cursor: AccessGrantCursor): Promise<AccessGrantsPage>;

    /**
     * Create new accessGrant
     *
     * @returns AccessGrant
     * @throws Error
     */
    create(projectId: string, name: string): Promise<AccessGrant>;

    /**
     * Delete existing access grant
     *
     * @returns null
     * @throws Error
     */
    delete(ids: string[]): Promise<void>;

    /**
     * Delete existing access grant by name and project id
     *
     * @returns null
     * @throws Error
     */
    deleteByNameAndProjectID(name: string, projectID: string): Promise<void>;

    /**
     * Get gateway credentials using access grant
     *
     * @returns EdgeCredentials
     * @throws Error
     */
    getGatewayCredentials(accessGrant: string, optionalURL?: string, isPublic?: boolean): Promise<EdgeCredentials>;
}

/**
 * Holds access grants sorting parameters.
 */
export enum AccessGrantsOrderBy {
    NAME = 1,
    CREATED_AT,
}

/**
 * AccessGrantCursor is a type, used to describe paged access grants list.
 */
export class AccessGrantCursor {
    public constructor(
        public search: string = '',
        public limit: number = 6,
        public page: number = 1,
        public order: AccessGrantsOrderBy = AccessGrantsOrderBy.NAME,
        public orderDirection: SortDirection = SortDirection.ASCENDING,
    ) {}
}

/**
 * AccessGrantsPage is a type, used to describe paged access grants list.
 */
export class AccessGrantsPage {
    public constructor(
        public accessGrants: AccessGrant[] = [],
        public search: string = '',
        public order: AccessGrantsOrderBy = AccessGrantsOrderBy.NAME,
        public orderDirection: SortDirection = SortDirection.ASCENDING,
        public limit: number = 6,
        public pageCount: number = 0,
        public currentPage: number = 1,
        public totalCount: number = 0,
    ) {}
}

/**
 * AccessGrant class holds info for Access Grant entity.
 */
export class AccessGrant {
    public isSelected: boolean;

    constructor(
        public id: string = '',
        public name: string = '',
        public createdAt: Date = new Date(),
        public secret: string = '',
    ) {
        this.isSelected = false;
    }

    /**
     * Returns created date as a local string.
     */
    public localDate(): string {
        return this.createdAt.toLocaleString('en-US', {timeZone: 'UTC', timeZoneName: 'short'});
    }
}

/**
 * DurationPermission class holds info for access grant's duration permission.
 */
export class DurationPermission {
    constructor(
        public notBefore: Date | null = null,
        public notAfter: Date | null = null,
    ) {}
}

/**
 * EdgeCredentials class holds info for edge credentials generated from access grant.
 */
export class EdgeCredentials {
    constructor(
        public id: string = '',
        public createdAt: Date = new Date(),
        public accessKeyId: string = '',
        public secretKey: string = '',
        public endpoint: string = '',
    ) {}
}
