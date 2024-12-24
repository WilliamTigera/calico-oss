# How to release Calico Enterprise

- [How to release Calico Enterprise](#how-to-release-calico-enterprise)
  - [Creating a release branch](#creating-a-release-branch)
    - [Prerequisite](#prerequisite)
    - [Code freeze](#code-freeze)
    - [Create branch](#create-branch)
    - [Code thaw](#code-thaw)
    - [Setup hashrelease](#setup-hashrelease)
      - [Update process repo](#update-process-repo)
      - [Create task in Semaphore](#create-task-in-semaphore)
  - [Performing a release](#performing-a-release)

## Creating a release branch

When preparing for a release, one of the earlier steps is to have a release branch created.

> [!CAUTION]
> This does not apply to patch releases and should be skipped.

### Prerequisite

1. Push access to protected branches for the following repos
   - `tigera/calico-private`
   - `tigera/manager`
   - `tigera/operator`

### Code freeze

Announce code freeze in [#eng-eng](https://tigera.slack.com/archives/GKTBUHGN4) slack channel. See sample below

```md
:rotating_light:  CODE FREEZE ALERT :cold_face: :rotating_light:

Consider the following branches are frozen for Enterprise <RELEASE_STREAM> branch cut:

- tigera/calico-private: master
- tigera/manager: master
- tigera/operator: master

unless... :index_pointing_at_the_viewer:
```

### Create branch

Follow these instructions for cutting new branch in both `tigera/calico-private` and `tigera/manager`.
For `tigera/operator` follow ["Preparing a new release branch"](https://github.com/tigera/operator/blob/master/RELEASING.md#preparing-a-new-release-branch) steps outlined in Tigera Operator `RELEASING.md`.

1. Checkout the latest master branch for calico-private (and manager)

    ```sh
    git fetch <remote>
    git switch -f -C master --track <remote>/master
    ```

1. Create new branch

    ```sh
    git checkout -b release-calient-vX.Y # if EP1 for vX.Y this should be git checkout -b release-calient-vX.Y-1
    ```

1. Update files to use new release branch(es) instead of master.

      > [!CAUTION]
      > This is only for c`tigera/calico-private`, skip for `tigera/manager`

   1. Update `OPERATOR_BRANCH` and `DEFAULT_BRANCH_OVERRIDE` in `metadata.mk`
   2. Run generation

      ```sh
      make generate
      ```

   3. Commit your changes

      ```sh
      git add .
      git commit -m "Updates for release-vX.Y'
      ```

   4. Push your changes

      ```sh
      git push <remote> release-vX.Y
      ```

2. Create an empty commit and tag in master

    ```sh
    git checkout <remote>/master
    git commit --allow-empty -m "Start development for vX.(Y+1)" # After cutting EP1, this will be git commit --allow-empty -m "Start development for vX.(Y+1)"
    git tag vX.(Y+1).0-calient-0.dev # git tag xX.Y.0-2.0-calient-0.dev
    git push <remote> master
    git push <remote> vX.(Y+1).0-calient-0.dev # git push <remote> xX.Y.0-2.0-calient-0.dev
    ```

### Code thaw

Add a message to the thread for the code freeze message from [earlier](#code-freeze) that the codes are unfrozen.

  > [!IMPORTANT]
  > Ensure the checkbox for "Also send to #eng-eng" is selected

```md
code freeze over! :melting_face:

All changes for Enterprise vX.Y should be committed to master branch and cherry-picked to release-calient-vX.Y branch
All Operator changes for Enterprise vX.Y should be committed to master branch and cherry-picked to release-vA.B branch
```

### Setup hashrelease

#### Update process repo

1. In `tigera/process`, create a new branch off the latest master

    ```sh
    git checkout -b release-calient-vX.Y # git checkout -b release-calient-vX.Y-1 for EP1
    ```

1. Rename the enterprise pipeline job and update the values (i.e. name, env_vars et. al)

    ```sh
    git mv .semaphore/create-hashrelease-calient-master.yml .semaphore/create-hashrelease.yml
    ```

1. Update the values in Makefile (including updating `go-build` version to the latest)

    > [!TIP]
    > Searching for "master" should give an idea of variables that need to be updates

1. Commit changes and push

    ```sh
    git add .
    git commit -m "Updates for release-calient-vX.Y" # git commit -m "Updates for release-calient-vX.Y-1" for EP1
    git push <remote> release-calient-vX.Y # git push <remote> release-calient-vX.Y-1 for EP1
    ```

#### Create task in Semaphore

1. Add a new task in [process Semaphore project](https://tigera.semaphoreci.com/projects/process/schedulers).

    > [!TIP]
    > [Enterprise v3.21 EP1](https://tigera.semaphoreci.com/projects/process/schedulers/fea71d3c-5dce-4f59-8219-c5ad85d1da93) is a good example,
    > be sure to use different times.

1. Hit "Run Now" and ensure the pipeline passes.

## Performing a release

Follow the instructions from [Calico Enterprise Release Cut Process](https://docs.google.com/document/d/1Oyg4avouWlLXLQf4wpDNsHzdmLyBfGuF_sFTs4t30ho/edit?usp=sharing)
