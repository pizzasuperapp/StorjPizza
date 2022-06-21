// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

export const NOTIFICATION_MUTATIONS = {
    ADD: 'ADD_NOTIFICATION',
    DELETE: 'DELETE_NOTIFICATION',
    PAUSE: 'PAUSE_NOTIFICATION',
    RESUME: 'RESUME_NOTIFICATION',
    CLEAR: 'CLEAR_NOTIFICATIONS',
};

export const APP_STATE_MUTATIONS = {
    TOGGLE_ADD_TEAMMEMBER_POPUP: 'TOGGLE_ADD_TEAMMEMBER_POPUP',
    TOGGLE_ACCOUNT_DROPDOWN: 'TOGGLE_ACCOUNT_DROPDOWN',
    TOGGLE_SELECT_PROJECT_DROPDOWN: 'TOGGLE_SELECT_PROJECT_DROPDOWN',
    TOGGLE_RESOURCES_DROPDOWN: 'TOGGLE_RESOURCES_DROPDOWN',
    TOGGLE_QUICK_START_DROPDOWN: 'TOGGLE_QUICK_START_DROPDOWN',
    TOGGLE_SETTINGS_DROPDOWN: 'TOGGLE_SETTINGS_DROPDOWN',
    TOGGLE_EDIT_PROJECT_DROPDOWN: 'TOGGLE_EDIT_PROJECT_DROPDOWN',
    TOGGLE_FREE_CREDITS_DROPDOWN: 'TOGGLE_FREE_CREDITS_DROPDOWN',
    TOGGLE_AVAILABLE_BALANCE_DROPDOWN: 'TOGGLE_AVAILABLE_BALANCE_DROPDOWN',
    TOGGLE_PERIODS_DROPDOWN: 'TOGGLE_PERIODS_DROPDOWN',
    TOGGLE_AG_DATEPICKER_DROPDOWN: 'TOGGLE_AG_DATEPICKER_DROPDOWN',
    TOGGLE_CHARTS_DATEPICKER_DROPDOWN: 'TOGGLE_CHARTS_DATEPICKER_DROPDOWN',
    TOGGLE_BUCKET_NAMES_DROPDOWN: 'TOGGLE_BUCKET_NAMES_DROPDOWN',
    TOGGLE_PERMISSIONS_DROPDOWN: 'TOGGLE_PERMISSIONS_DROPDOWN',
    TOGGLE_SUCCESSFUL_PASSWORD_RESET: 'TOGGLE_SUCCESSFUL_PASSWORD_RESET',
    TOGGLE_SUCCESSFUL_PROJECT_CREATION_POPUP: 'TOGGLE_SUCCESSFUL_PROJECT_CREATION_POPUP',
    TOGGLE_EDIT_PROFILE_POPUP: 'TOGGLE_EDIT_PROFILE_POPUP',
    TOGGLE_CHANGE_PASSWORD_POPUP: 'TOGGLE_CHANGE_PASSWORD_POPUP',
    TOGGLE_UPLOAD_CANCEL_POPUP: 'TOGGLE_UPLOAD_CANCEL_POPUP',
    TOGGLE_CREATE_PROJECT_PROMPT_POPUP: 'TOGGLE_CREATE_PROJECT_PROMPT_POPUP',
    TOGGLE_IS_ADD_PM_MODAL_SHOWN: 'TOGGLE_IS_ADD_PM_MODAL_SHOWN',
    TOGGLE_OPEN_BUCKET_MODAL_SHOWN: 'TOGGLE_OPEN_BUCKET_MODAL_SHOWN',
    SHOW_DELETE_PAYMENT_METHOD_POPUP: 'SHOW_DELETE_PAYMENT_METHOD_POPUP',
    SHOW_SET_DEFAULT_PAYMENT_METHOD_POPUP: 'SHOW_SET_DEFAULT_PAYMENT_METHOD_POPUP',
    CLOSE_ALL: 'CLOSE_ALL',
    CHANGE_STATE: 'CHANGE_STATE',
    TOGGLE_PAYMENT_SELECTION: 'TOGGLE_PAYMENT_SELECTION',
    SET_SATELLITE_NAME: 'SET_SATELLITE_NAME',
    SET_PARTNERED_SATELLITES: 'SET_PARTNERED_SATELLITES',
    SET_SATELLITE_STATUS: 'SET_SATELLITE_STATUS',
    SET_COUPON_CODE_BILLING_UI_STATUS: 'SET_COUPON_CODE_BILLING_UI_STATUS',
    SET_COUPON_CODE_SIGNUP_UI_STATUS: 'SET_COUPON_CODE_SIGNUP_UI_STATUS',
    SET_PROJECT_DASHBOARD_STATUS: 'SET_PROJECT_DASHBOARD_STATUS',
    SET_ONB_AG_NAME_STEP_BACK_ROUTE: 'SET_ONB_AG_NAME_STEP_BACK_ROUTE',
    SET_ONB_API_KEY_STEP_BACK_ROUTE: 'SET_ONB_API_KEY_STEP_BACK_ROUTE',
    SET_ONB_API_KEY: 'SET_ONB_API_KEY',
    SET_ONB_CLEAN_API_KEY: 'SET_ONB_CLEAN_API_KEY',
    SET_OBJECTS_FLOW_STATUS: 'SET_OBJECTS_FLOW_STATUS',
    SET_ONB_OS: 'SET_ONB_OS',
};