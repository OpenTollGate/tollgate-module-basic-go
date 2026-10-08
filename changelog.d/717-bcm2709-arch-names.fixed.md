- **The bcm2709 artifact rows build again.** Two arch-naming bugs only
  this target can hit: `build-sdk-package.sh` defaulted bcm2709 to
  `EXPECTED_ARCH=arm_cortex-a7`, but the SDK stages packages under
  `arm_cortex-a7_neon-vfpv4` (the staged-dir check failed on an
  otherwise-clean cross-compile); and the apk path's post-make gate
  globbed `/builder/build_dir/target-${EXPECTED_ARCH}_*`, which can
  never match a `+`-feature arch (build_dir spells them
  `arm_cortex-a7+neon-vfpv4`) — bcm2709 failed *after* a successful
  build. Both fixed; the rc1 matrix's two missing rows build (12/12)
  ([#717](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/717)).
