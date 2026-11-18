/** The one orchestrated moment: a verdict settling in when a customer is looked up. Everything else moves only when
    someone opens or closes it. */
export const settle = { type: "spring", stiffness: 320, damping: 32 } as const;
export const open = { duration: 0.18 } as const;
