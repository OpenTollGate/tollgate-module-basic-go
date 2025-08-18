# Design Document: Building and Deploying the Tollgate Captive Portal

## Objective

This document outlines the steps required to build the Tollgate Captive Portal React application and deploy its production build to the `files/tollgate-captive-portal-site` directory within the `tollgate-module-basic-go` repository. This ensures that the latest version of the captive portal is included in the OpenWRT package.

## Prerequisites

*   Node.js (v14 or later)
*   npm (v6 or later)
*   Access to the `tollgate-captive-portal-site` repository.

## Steps

1.  **Navigate to the Captive Portal Project Directory:**
    Change the current working directory to the `tollgate-captive-portal-site` repository where the `package.json` file is located.

    ```bash
    cd /home/c03rad0r/TG/tollgate-captive-portal-site
    ```

2.  **Install Dependencies:**
    Install all necessary Node.js dependencies for the React project.

    ```bash
    npm install
    ```

3.  **Build the Project:**
    Create a production-ready build of the React application. This will generate optimized static files in the `build` directory.

    ```bash
    npm run build
    ```

4.  **Remove Existing Captive Portal Files (Optional but Recommended):**
    Before copying the new build, it's good practice to remove the existing files in the destination directory to ensure a clean deployment and avoid conflicts from old files.

    ```bash
    rm -rf /home/c03rad0r/TG/tollgate-module-basic-go/files/tollgate-captive-portal-site/*
    ```
    Note: This command will delete all files and subdirectories within the specified path. Ensure the path is correct before execution.

5.  **Copy Build Output to Destination:**
    Copy the entire contents of the `build` directory (generated in step 3) to the target directory within the `tollgate-module-basic-go` repository.

    ```bash
    cp -r build/* /home/c03rad0r/TG/tollgate-module-basic-go/files/tollgate-captive-portal-site/
    ```

6.  **Verify Deployment:**
    List the contents of the target directory to confirm that the new build files have been successfully copied.

    ```bash
    ls -l /home/c03rad0r/TG/tollgate-module-basic-go/files/tollgate-captive-portal-site/
    ```

## Expected Outcome

Upon successful execution of these steps, the `files/tollgate-captive-portal-site` directory in the `tollgate-module-basic-go` repository will contain the latest production build of the Tollgate Captive Portal, ready for inclusion in the OpenWRT package.